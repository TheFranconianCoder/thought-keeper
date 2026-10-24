package usecases

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// AttachURLSnapshot stores a page the HUMAN supplied for a URL the app could not read: a login wall, a
// paywall, a JS application. The app does not authenticate, and it never will — but the human is
// already logged in, in their browser. Saving the page (HTML or PDF) and attaching it is the whole
// exchange: the human does the authenticated part, the app does the reading.
//
// The URL stays the ADDRESS of the source (one source per URL, everything that cites it survives), and
// the attached file becomes its snapshot — the same place a fetch writes. The record says it was
// attached, with the file's name and hash, and the refresh leaves such a source alone: a login page is
// not a correction of the article.
type AttachURLSnapshot struct {
	// Convert turns an attached HTML page into Markdown (the same readability path a fetch uses).
	Convert interfaces.HTMLConverter
	// Extractor reads an attached PDF (the text layer, or OCR) into parts.
	Extractor interfaces.Extractor
	Store     interfaces.SourceStore
	Index     interfaces.IndexRepository
	Steps     interfaces.StepTracker
	SourceLog interfaces.SourceLog
	Languages interfaces.LanguageDetector
	Clock     interfaces.Clock
	Log       *slog.Logger
	// MaxBytes bounds one attached file. Zero means 32 MiB (the PDF ceiling is larger than a fetch's,
	// because a page saved as PDF carries its own layout).
	MaxBytes int64
}

// AttachInput is one human-supplied snapshot.
type AttachInput struct {
	// Hash addresses the source to fill: its URL hash. The row must exist — attaching is not another
	// way to add a source, it is the way to give one its bytes.
	Hash string
	// Name is the file's own name; its extension selects the conversion.
	Name string
	Data []byte
}

// attachedDocument is what one conversion produced: the readable body and what the file itself named.
type attachedDocument struct {
	Body    string
	Title   string
	Excerpt string
	Site    string
	Pages   int
	OCR     bool
}

// Execute stores the attachment as the URL source's new snapshot and re-arms the document steps.
func (uc AttachURLSnapshot) Execute(ctx context.Context, in AttachInput) (partialentities.PartialSource, error) {
	src, err := uc.Index.GetSourceByHash(ctx, strings.TrimSpace(in.Hash))
	if err != nil {
		return partialentities.PartialSource{}, types.New("not_found", "the source does not exist", types.ErrNotFound)
	}
	if src.Kind != entities.SourceKindURL {
		return partialentities.PartialSource{}, types.New(
			"unsupported_source", "only a URL source can take an attached snapshot", types.ErrUnsupportedSource)
	}
	if src.Sitemap {
		return partialentities.PartialSource{}, types.New(
			"unsupported_source", "a sitemap is a page list, not a document", types.ErrUnsupportedSource)
	}
	if src.RSS {
		return partialentities.PartialSource{}, types.New(
			"unsupported_source", "a feed is an item list, not a document", types.ErrUnsupportedSource)
	}
	name := filepath.Base(strings.TrimSpace(in.Name))
	if name == "" {
		return partialentities.PartialSource{}, types.New(
			"unsupported_source", "the attached file has no name", types.ErrUnsupportedSource)
	}
	if len(in.Data) == 0 {
		return partialentities.PartialSource{}, types.New(
			"unsupported_source", "the attached file is empty", types.ErrUnsupportedSource)
	}
	limit := uc.MaxBytes
	if limit <= 0 {
		limit = 32 << 20
	}
	if int64(len(in.Data)) > limit {
		return partialentities.PartialSource{}, types.New(
			"unsupported_source", fmt.Sprintf("the attached file is larger than %d bytes", limit),
			types.ErrUnsupportedSource)
	}

	doc, err := uc.convert(ctx, src, name, in.Data)
	if err != nil {
		return partialentities.PartialSource{}, err
	}
	doc.Title = firstNonEmpty(doc.Title, src.Title, src.URL)
	body := strings.TrimSpace(doc.Body)
	if body == "" {
		return partialentities.PartialSource{}, types.New(
			"unsupported_source", "the attached file has no readable text", types.ErrUnsupportedSource)
	}

	sum := sha256.Sum256(in.Data)
	sha := hex.EncodeToString(sum[:])
	language := ""
	if uc.Languages != nil {
		language = uc.Languages.Detect(body)
	}

	now := uc.Clock.Now()
	snapshot := renderAttachedSnapshot(src.URL, doc.Title, name, sha, now, body)
	obj, err := uc.Store.PutAt(src.Hash, ".md", []byte(snapshot))
	if err != nil {
		return partialentities.PartialSource{}, err
	}

	src.Ext, src.Path, src.Bytes = obj.Ext, obj.Path, obj.Bytes
	src.Title, src.Bookmark, src.Language = doc.Title, false, language
	// The human's snapshot is the source now; a feed's copy is history.
	src.FromFeed, src.FeedHash = false, ""
	src.Meta = metaWithIdentity(metaWith(src.Meta, map[string]any{
		"url": src.URL, "attached": true, "attached_from": name, "attached_hash": sha,
		"content_hash": contentHashOf(doc.Title, body), "last_error": nil, "wall": nil,
		"from_feed": nil, "feed_hash": nil,
	}), src)
	if doc.Excerpt != "" {
		src.Meta = metaWith(src.Meta, map[string]any{"excerpt": doc.Excerpt})
	}
	if doc.Site != "" {
		src.Meta = metaWith(src.Meta, map[string]any{"site": doc.Site})
	}
	if err := uc.Index.FillURLSource(ctx, src); err != nil {
		return partialentities.PartialSource{}, err
	}
	_ = uc.Index.SetSourceTitle(ctx, src.Hash, doc.Title)
	_ = uc.Index.SetSourceMeta(ctx, src.Hash, src.Meta)
	// There was no HTTP answer for THESE bytes. Zero is "unknown", which is the truth.
	_ = uc.Index.SetSourceURLState(ctx, src.Hash, 0, now)
	if language != "" {
		_ = uc.Index.SetSourceLanguage(ctx, src.Hash, language)
	}
	if uc.Steps != nil {
		// The old derivation was built on other bytes: reset it (capture survives, outline and
		// distill are re-armed) and record the capture that just happened.
		icon := faviconFromMeta(src.Meta)
		_ = uc.Steps.Reset(src.Hash)
		_ = uc.Steps.SetLanguage(src.Hash, language)
		_ = uc.Steps.SetIdentity(src.Hash, entities.SourceIdentity{
			Kind: entities.SourceKindURL, URL: src.URL, Language: language,
			OriginalName: src.URL, ContentHash: contentHashOf(doc.Title, body),
			Attached: true, AttachedFrom: name, AttachedHash: sha,
			Origin: src.Origin, WikiPage: src.WikiPage, ParentHash: src.ParentHash, Sitemap: src.Sitemap,
			FaviconHash: faviconHash(icon), FaviconExt: faviconExt(icon),
		})
	}
	recordStep(ctx, uc.Steps, uc.Clock, src.Hash, entities.StepCapture, entities.StepOK,
		"attached · "+name, nil)
	appendSourceLog(ctx, uc.SourceLog, uc.Clock, src.Hash, "capture", "info", "snapshot attached",
		map[string]any{
			"file": name, "bytes": len(in.Data), "sha256": sha,
			"pages": doc.Pages, "ocr": doc.OCR, "chars": len(body),
		})
	if uc.Log != nil {
		uc.Log.Info("url: snapshot attached", "url", src.URL, "hash", src.Hash,
			"file", name, "bytes", len(in.Data))
	}

	stored, err := uc.Index.GetSourceByHash(ctx, src.Hash)
	if err != nil {
		stored = src
	}
	return partialentities.PartialFromSource(stored), nil
}

// convert turns the attached file into Markdown, by the same paths the rest of the pipeline uses:
// HTML through readability, a PDF through poppler/OCR, Markdown and text as they are.
func (uc AttachURLSnapshot) convert(ctx context.Context, src entities.Source, name string, data []byte) (attachedDocument, error) {
	ext := strings.ToLower(filepath.Ext(name))
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	switch ext {
	case ".html", ".htm":
		if uc.Convert == nil {
			return attachedDocument{}, types.New("not_implemented", "HTML conversion is not configured", types.ErrNotImplemented)
		}
		conv, err := uc.Convert.Convert(ctx, data, src.URL)
		if err != nil {
			return attachedDocument{}, types.New(
				"unsupported_source", "the attached HTML could not be read: "+err.Error(), types.ErrUnsupportedSource)
		}
		return attachedDocument{
			Body: conv.Markdown, Title: firstNonEmpty(conv.Title, src.Title, src.URL),
			Excerpt: conv.Excerpt, Site: conv.SiteName,
		}, nil
	case ".md", ".markdown":
		text, dropped := stripLeadingFrontmatter(normalizeLines(string(data)))
		if dropped && uc.Log != nil {
			uc.Log.Debug("attach: the file's own frontmatter was dropped", "file", name)
		}
		named := src
		named.Kind, named.Title = entities.SourceKindMD, stem
		title, _ := DocumentTitle(named, nil, text)
		return attachedDocument{Body: text, Title: title}, nil
	case ".txt":
		text := normalizeLines(string(data))
		named := src
		named.Kind, named.Title = entities.SourceKindTXT, stem
		title, _ := DocumentTitle(named, nil, text)
		return attachedDocument{Body: text, Title: title}, nil
	case ".pdf":
		if uc.Extractor == nil {
			return attachedDocument{}, types.New("not_implemented", "PDF extraction is not configured", types.ErrNotImplemented)
		}
		parts, err := uc.Extractor.Extract(ctx, ".pdf", data, nil)
		if err != nil {
			return attachedDocument{}, err
		}
		pages, ocr := summarize(parts)
		return attachedDocument{
			Body: renderAttachedParts(parts), Title: stem, Pages: pages, OCR: ocr,
		}, nil
	default:
		return attachedDocument{}, types.New(
			"unsupported_source", "attach a .html, .pdf, .md or .txt file", types.ErrUnsupportedSource)
	}
}

// renderAttachedParts writes an extracted document as plain Markdown: a heading per titled part, its
// text underneath, nothing else. The capture rendering's part markers are deliberately NOT used here —
// this body is read by the MARKDOWN extractor, which would keep a comment line as text.
func renderAttachedParts(parts []entities.Part) string {
	var b strings.Builder
	for _, p := range parts {
		title := strings.TrimSpace(p.Title)
		text := strings.TrimSpace(p.Text)
		if title == "" && text == "" {
			continue
		}
		if title != "" {
			b.WriteString(strings.Repeat("#", min(max(p.Level, 1), 6)))
			b.WriteString(" ")
			b.WriteString(title)
			b.WriteString("\n\n")
		}
		if text != "" {
			b.WriteString(text)
			b.WriteString("\n\n")
		}
	}
	return b.String()
}

// stripLeadingFrontmatter drops a file's own YAML block (a Markdown file saved from a notes app) so it
// cannot sit above the snapshot's frontmatter. The keys are not decoded — the core never parses YAML —
// and the caller logs that they were dropped rather than pretending they are still there.
func stripLeadingFrontmatter(s string) (string, bool) {
	lines := strings.Split(s, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return s, false
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.Join(lines[i+1:], "\n"), true
		}
	}
	return s, false
}

// normalizeLines keeps the text as the human saved it, only normalising the line endings (a browser
// save writes CRLF, and a snapshot is read by parsers that expect "\n").
func normalizeLines(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// renderAttachedSnapshot is the snapshot a human's file becomes: the URL it belongs to, the file it
// came from, and the text. There is no http_status and no fetched_at, because nothing was fetched.
func renderAttachedSnapshot(rawURL, title, name, sha string, at time.Time, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "url: %s\n", rawURL)
	fmt.Fprintf(&b, "attached_at: %s\n", at.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "attached_from: %s\n", yamlScalar(name))
	fmt.Fprintf(&b, "title: %s\n", yamlScalar(title))
	fmt.Fprintf(&b, "attached_sha256: %s\n", sha)
	b.WriteString("attached: true\n")
	b.WriteString("---\n\n")
	b.WriteString(body)
	b.WriteString("\n")
	return b.String()
}
