package usecases

import (
	"bytes"
	"compress/gzip"
	"testing"
)

func TestParseSitemapURLSetAndIndex(t *testing.T) {
	urlset := []byte(`<urlset><url><loc>https://example.com/a</loc></url></urlset>`)
	got, ok := parseSitemap(urlset, "https://example.com/sitemap.xml")
	if !ok || len(got) != 1 || got[0] != "https://example.com/a" {
		t.Fatalf("urlset = %v, %v", got, ok)
	}

	index := []byte(`<sitemapindex><sitemap><loc>https://example.com/child.xml</loc></sitemap></sitemapindex>`)
	got, ok = parseSitemap(index, "https://example.com/sitemap.xml")
	if !ok || len(got) != 1 || got[0] != "https://example.com/child.xml" {
		t.Fatalf("index = %v, %v", got, ok)
	}

	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, _ = writer.Write(urlset)
	_ = writer.Close()
	got, ok = parseSitemap(compressed.Bytes(), "https://example.com/sitemap.xml")
	if !ok || len(got) != 1 || got[0] != "https://example.com/a" {
		t.Fatalf("gzip = %v, %v", got, ok)
	}
}
