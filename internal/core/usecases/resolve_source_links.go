package usecases

import (
	"context"
	"net/url"
	"strings"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// SourceLinkResolver resolves the URLs a rendered document links to. A handler holds this value;
// nil means every link keeps opening the web.
type SourceLinkResolver func(ctx context.Context, urls []string) ([]entities.SourceLink, error)

// resolveLinksLimit bounds one pass: a document links to a handful of pages, and a snapshot that
// lists thousands of them is read as the screen shows it, not as a whole.
const (
	resolveLinksLimit = 500
	resolveLinksChunk = 400
)

// ResolveLinks maps each URL to the source the vault already holds for it. A URL nothing knows is
// simply absent from the answer: the link keeps opening the web. Matching is by entities.URLKey, so
// a fragment or a trailing slash does not turn the same page into a stranger.
func (uc Sources) ResolveLinks(ctx context.Context, urls []string) ([]entities.SourceLink, error) {
	inputs := make([]string, 0, len(urls))
	seen := make(map[string]bool, len(urls))
	for _, raw := range urls {
		value := strings.TrimSpace(raw)
		if !isHTTPURL(value) {
			continue
		}
		key := entities.URLKey(value)
		if seen[key] {
			continue
		}
		seen[key] = true
		inputs = append(inputs, value)
		if len(inputs) == resolveLinksLimit {
			break
		}
	}
	if len(inputs) == 0 {
		return nil, nil
	}

	// The stored address may carry a trailing slash or a fragment the link does not. Query the
	// candidate raw forms once, then let the URL key decide.
	candidates := make([]string, 0, len(inputs)*4)
	seenCandidate := make(map[string]bool, len(inputs)*4)
	for _, input := range inputs {
		for _, candidate := range urlCandidates(input) {
			if seenCandidate[candidate] {
				continue
			}
			seenCandidate[candidate] = true
			candidates = append(candidates, candidate)
		}
	}

	known := make(map[string]string, len(inputs))
	for start := 0; start < len(candidates); start += resolveLinksChunk {
		end := min(start+resolveLinksChunk, len(candidates))
		rows, err := uc.Index.ListSourceLinks(ctx, candidates[start:end])
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			key := entities.URLKey(row.URL)
			if _, ok := known[key]; !ok {
				known[key] = row.Hash
			}
		}
	}

	out := make([]entities.SourceLink, 0, len(inputs))
	for _, input := range inputs {
		if hash, ok := known[entities.URLKey(input)]; ok {
			out = append(out, entities.SourceLink{URL: input, Hash: hash})
		}
	}
	return out, nil
}

func isHTTPURL(value string) bool {
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")
}

// urlCandidates are the raw forms a stored address may have: as written, without the fragment, and
// with or without the trailing slash URLKey ignores.
func urlCandidates(raw string) []string {
	out := []string{raw}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return out
	}
	parsed.Fragment = ""
	parsed.RawFragment = ""
	out = append(out, parsed.String())
	if path, ok := strings.CutSuffix(parsed.Path, "/"); ok {
		parsed.Path = path
		out = append(out, parsed.String())
	} else if parsed.Path != "" {
		parsed.Path += "/"
		out = append(out, parsed.String())
	}
	return out
}
