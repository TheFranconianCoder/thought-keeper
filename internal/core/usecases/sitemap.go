package usecases

import (
	"bytes"
	"compress/gzip"
	"encoding/xml"
	"io"
	"net/url"
	"strings"
)

// parseSitemap recognises the two sitemap documents the app accepts and returns the URLs they
// declare. A `<urlset>` lists pages; a `<sitemapindex>` lists other sitemaps. Both are treated the
// same here: the caller claims them as children of the sitemap that named them, and a child sitemap
// expands in turn when its fetch completes.
//
// URLs are resolved against the sitemap address (the spec asks for absolute locations, but a
// relative one is still usable), restricted to http/https and deduplicated. A URL naming the
// sitemap itself is dropped: a child that owns its own parent would make the ownership graph a
// cycle.
func parseSitemap(body []byte, baseURL string) ([]string, bool) {
	body = gunzipSitemap(body)
	var root struct {
		XMLName xml.Name
	}
	if err := xml.Unmarshal(body, &root); err != nil {
		return nil, false
	}

	var locations []string
	switch root.XMLName.Local {
	case "urlset":
		var set struct {
			URLs []struct {
				Loc string `xml:"loc"`
			} `xml:"url"`
		}
		if err := xml.Unmarshal(body, &set); err != nil {
			return nil, false
		}
		for _, entry := range set.URLs {
			locations = append(locations, entry.Loc)
		}
	case "sitemapindex":
		var index struct {
			Sitemaps []struct {
				Loc string `xml:"loc"`
			} `xml:"sitemap"`
		}
		if err := xml.Unmarshal(body, &index); err != nil {
			return nil, false
		}
		for _, entry := range index.Sitemaps {
			locations = append(locations, entry.Loc)
		}
	default:
		return nil, false
	}

	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, false
	}
	self := strings.TrimSuffix(base.String(), "#")
	seen := make(map[string]bool, len(locations))
	out := make([]string, 0, len(locations))
	for _, raw := range locations {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		ref, err := url.Parse(raw)
		if err != nil {
			continue
		}
		resolved := base.ResolveReference(ref)
		if resolved.Scheme != "http" && resolved.Scheme != "https" {
			continue
		}
		resolved.Fragment = ""
		value := resolved.String()
		if value == self || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out, true
}

// gunzipSitemap accepts the common sitemap.xml.gz form. The fetcher decompresses a
// Content-Encoding: gzip response itself; this covers a .gz FILE served as an ordinary body.
func gunzipSitemap(body []byte) []byte {
	if len(body) < 2 || body[0] != 0x1f || body[1] != 0x8b {
		return body
	}
	reader, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return body
	}
	defer func() { _ = reader.Close() }()
	out, err := io.ReadAll(io.LimitReader(reader, 20<<20))
	if err != nil {
		return body
	}
	return out
}
