// Package sourcemeta persists the state of a source's DOCUMENT steps (capture, outline, distill)
// next to the source object itself, as sources/<ab>/<cd>/<hash>.meta.
//
// It lives in the VAULT on purpose. The four steps are the pipeline's contract with the reader, and
// what a document has been through must survive a lost index: the database is a performance layer
// that is rebuilt from the files, never the other way round. With the .meta file in place the
// question "was this document captured, outlined, distilled — and did any of it fail?" is answered
// by reading the vault, not by trusting a cache.
package sourcemeta

import (
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// Ext is the sidecar's extension: <hash>.meta next to <hash>.pdf.
const Ext = entities.SidecarMeta

// Record is one source's document-step state — and, because the file is the truth about the source,
// its IDENTITY too. The object's file name cannot carry the kind any more: a URL snapshot and a
// Markdown source are both <hash>.md (and a PDF's rendering is a third one), so "what is this?" is
// answered here, not by an extension.
type Record struct {
	Hash     string `json:"hash"`
	Kind     string `json:"kind,omitempty"`
	URL      string `json:"url,omitempty"`
	Language string `json:"language,omitempty"`
	// Origin/WikiPage mark a source created by a wiki page (for safe removal when the URL leaves
	// the page). ParentHash owns a sitemap page; Sitemap marks the sitemap itself.
	Origin     string `json:"origin,omitempty"`
	WikiPage   string `json:"wiki_page,omitempty"`
	ParentHash string `json:"parent_hash,omitempty"`
	Sitemap    bool   `json:"sitemap,omitempty"`
	// RSS marks a source that is itself a feed (see entities.Source.RSS). Like Sitemap it is part of
	// what the source IS, so a reindex keeps it.
	RSS bool `json:"rss,omitempty"`
	// Bookmark marks a collected link: metadata only, never distilled.
	Bookmark     bool   `json:"bookmark,omitempty"`
	OriginalName string `json:"original_name,omitempty"`
	Title        string `json:"title,omitempty"`
	// Tags are the human's labels for this source: a RECORD, like the title, not derived state. The
	// vocabulary observes them under the source-tags namespace.
	Tags []string `json:"tags,omitempty"`
	// AutoTags are the agent's labels for this source; AutoTagsAt is when they were derived (Unix
	// millis, 0 = never). They are a derived CACHE in a record: a reindex restores them, a reset
	// clears them, and the consistency job owes a never-tagged source a pass.
	AutoTags   []string `json:"auto_tags,omitempty"`
	AutoTagsAt int64    `json:"auto_tags_at,omitempty"`
	// ContentHash is the URL SNAPSHOT's hash — the refresher's "same page or changed page". It lives
	// in the vault because the index is disposable: with it only in the index, a reindex made every
	// URL look changed and the next refresh filed a second snapshot for each of them.
	ContentHash string `json:"content_hash,omitempty"`
	// Attached marks a snapshot the human supplied (see SourceIdentity.Attached). It is stored here,
	// not only in the index, so a reindex cannot turn an attached article back into a page the next
	// refresh is allowed to overwrite.
	Attached     bool   `json:"attached,omitempty"`
	AttachedFrom string `json:"attached_from,omitempty"`
	AttachedHash string `json:"attached_hash,omitempty"`
	// FromFeed/FeedHash mark a snapshot taken from a feed's copy of the post.
	FromFeed bool   `json:"from_feed,omitempty"`
	FeedHash string `json:"feed_hash,omitempty"`
	// FaviconHash/FaviconExt point at the site's stored icon (the bytes are derived and disposable,
	// the reference is not).
	FaviconHash string `json:"favicon_hash,omitempty"`
	FaviconExt  string `json:"favicon_ext,omitempty"`
	// Steps holds capture, outline and distill. compile is deliberately NOT here: it runs over the
	// whole vault, so it is no property of a single source (per source it is derived: are this
	// source's atoms on a page yet?).
	Steps entities.StepSet `json:"steps"`
	// Parts is the record behind the distill step: which text-bearing node is finished, and why not.
	Parts   map[string]entities.PartRecord `json:"parts,omitempty"`
	Updated time.Time                      `json:"updated"`
}

// SetParts merges a run's part records into the source's record.
func (s *Store) SetParts(hash string, parts map[string]entities.PartRecord) (Record, error) {
	return s.Update(hash, func(rec *Record) error {
		if rec.Parts == nil {
			rec.Parts = map[string]entities.PartRecord{}
		}
		maps.Copy(rec.Parts, parts)
		return nil
	})
}

// Store reads and writes the .meta sidecars of one sources/ directory.
type Store struct {
	root string
	// mu guards locks; locks holds one mutex per source, so the read-modify-write of a record is
	// serialised without serialising the whole vault.
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// New returns a store for the vault's sources/ directory.
func New(sourcesDir string) *Store { return &Store{root: sourcesDir} }

// Path is the sidecar's path for a hash.
func (s *Store) Path(hash string) string { return objects.SidecarPath(s.root, hash, Ext) }

// Read returns a source's record. A missing file is not an error: it is a source whose document
// steps have never been recorded (an older vault), and the caller treats it as all-pending.
func (s *Store) Read(hash string) (Record, error) {
	b, err := os.ReadFile(s.Path(hash))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Record{Hash: hash, Steps: entities.NewStepSet()}, nil
		}
		return Record{}, err
	}
	var rec Record
	if err := json.Unmarshal(b, &rec); err != nil {
		// A corrupt sidecar must not take the vault down: it is derived bookkeeping, and the
		// source itself is untouched. Report it as unrecorded and let the next write repair it.
		return Record{Hash: hash, Steps: entities.NewStepSet()}, nil
	}
	if rec.Steps == nil {
		rec.Steps = entities.NewStepSet()
	}
	if rec.Parts == nil {
		rec.Parts = map[string]entities.PartRecord{}
	}
	if rec.Hash == "" {
		rec.Hash = hash
	}
	return rec, nil
}

// Write stores a record atomically (temp file → rename).
//
// The temp file has a UNIQUE name. It used to be `<hash>.meta.tmp`: two writers for the same source
// (a step record and a language backfill, a fetch and its step) wrote into the SAME file, and the
// first rename could publish the other one's half-written bytes — a truncated record that read as
// "unrecorded" and silently took the source's identity with it.
func (s *Store) Write(rec Record) error {
	if !entities.IsSHA256Hex(rec.Hash) {
		return types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash)
	}
	path := s.Path(rec.Hash)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if rec.Updated.IsZero() {
		rec.Updated = s.now()
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+rec.Hash[:8]+"-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// Update applies fn to one source's record under that source's own lock: read, change, write.
//
// The lock is what the file-based bookkeeping needs. Read-modify-write without it lost fields: two
// steps finishing at the same moment each started from the record they had read, and the second write
// dropped the first step's state. The lock is per hash, so records of different sources stay parallel.
func (s *Store) Update(hash string, fn func(*Record) error) (Record, error) {
	unlock := s.lock(hash)
	defer unlock()
	rec, err := s.Read(hash)
	if err != nil {
		return Record{}, err
	}
	if err := fn(&rec); err != nil {
		return rec, err
	}
	rec.Hash = hash
	rec.Updated = s.now()
	if err := s.Write(rec); err != nil {
		return rec, err
	}
	return rec, nil
}

// lock serialises the read-modify-write of ONE source's record.
func (s *Store) lock(hash string) func() {
	s.mu.Lock()
	if s.locks == nil {
		s.locks = make(map[string]*sync.Mutex)
	}
	l, ok := s.locks[hash]
	if !ok {
		l = &sync.Mutex{}
		s.locks[hash] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// now is the record's clock. A sidecar carries UTC timestamps like everything else in the vault.
func (s *Store) now() time.Time { return time.Now().UTC() }

// Set records one step's status (read → merge → write) and returns the merged record.
func (s *Store) Set(hash string, step entities.Step, st entities.StepStatus) (Record, error) {
	return s.Update(hash, func(rec *Record) error {
		rec.Steps = rec.Steps.With(step, st)
		return nil
	})
}

// Remove drops a source's sidecar (the source itself is gone).
func (s *Store) Remove(hash string) error {
	if err := os.Remove(s.Path(hash)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Scan reads every sidecar in the store, keyed by hash. It is how a reindex rebuilds the step
// state from the vault alone.
func (s *Store) Scan() (map[string]Record, error) {
	out := map[string]Record{}
	err := filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != s.root {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), Ext) {
			return nil
		}
		hash := strings.TrimSuffix(d.Name(), Ext)
		if !entities.IsSHA256Hex(hash) {
			return nil
		}
		rec, err := s.Read(hash)
		if err != nil {
			return nil
		}
		out[hash] = rec
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
