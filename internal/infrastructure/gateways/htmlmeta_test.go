package gateways

import "testing"

const pageHTML = `<!doctype html>
<html lang="de">
<head>
  <title>Der echte Titel | Beispielseite</title>
  <meta property="og:title" content="Der echte Titel">
  <meta property="og:description" content="Eine kurze Beschreibung.">
  <meta property="og:site_name" content="Beispielseite">
</head>
<body><h1>Erste Überschrift</h1><p>Text</p></body>
</html>`

func TestExtractPageMetaPrefersDeclaredTitle(t *testing.T) {
	meta := extractPageMeta([]byte(pageHTML))
	if got := meta.bestTitle(); got != "Der echte Titel" {
		t.Errorf("title = %q, want the og:title", got)
	}
	if meta.bestDescription() != "Eine kurze Beschreibung." {
		t.Errorf("description = %q", meta.bestDescription())
	}
	if meta.SiteName != "Beispielseite" {
		t.Errorf("site = %q", meta.SiteName)
	}
	if meta.Language != "de" {
		t.Errorf("language = %q", meta.Language)
	}
}

func TestExtractPageMetaFallsBackToTitleTag(t *testing.T) {
	raw := `<html><head><title>  Nur   der Title  </title></head><body><h1>Heading</h1></body></html>`
	meta := extractPageMeta([]byte(raw))
	if got := meta.bestTitle(); got != "Nur der Title" {
		t.Errorf("title = %q, want the <title> (whitespace collapsed), not the heading", got)
	}
}

func TestConvertUsesTitleTagNotHeading(t *testing.T) {
	conv := ReadabilityConverter{}
	out, err := conv.Convert(t.Context(), []byte(pageHTML), "https://example.com/a")
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if out.Title != "Der echte Titel" {
		t.Errorf("converted title = %q, want the declared title", out.Title)
	}
}

// TestPageDatePrefersThePagesOwnStatement: a page's date is its metadata first (an article meta tag,
// an itemprop, a <time datetime>), and only then the line under the title. A modified date is not a
// publication, so a page that only declares one gets no date from its head.
func TestPageDatePrefersThePagesOwnStatement(t *testing.T) {
	metaHTML := `<html><head>
  <meta property="article:published_time" content="2026-10-27T07:36:09+02:00">
</head><body><p>Text</p></body></html>`
	meta := extractPageMeta([]byte(metaHTML))
	if meta.Published.IsZero() || meta.Published.UTC().Format("2006-01-02") != "2026-10-27" {
		t.Fatalf("meta date = %v (%q), want 2026-10-27 from article:published_time", meta.Published, meta.PublishedFrom)
	}
	if meta.PublishedFrom != "meta" {
		t.Errorf("from = %q, want meta", meta.PublishedFrom)
	}

	itempropHTML := `<html><head>
  <meta itemprop="datePublished" content="27. Oktober 2026">
</head><body><p>Text</p></body></html>`
	if meta := extractPageMeta([]byte(itempropHTML)); meta.Published.Format("2006-01-02") != "2026-10-27" {
		t.Fatalf("itemprop date = %v, want 2026-10-27", meta.Published)
	}

	timeHTML := `<html><head><title>Der echte Titel</title></head>
<body><article><h1>Der echte Titel</h1><p><time datetime="2026-10-27">27. Oktober 2026</time></p><p>Text</p></article></body></html>`
	if meta := extractPageMeta([]byte(timeHTML)); meta.Published.Format("2006-01-02") != "2026-10-27" || meta.PublishedFrom != "time" {
		t.Fatalf("time date = %v (%q), want 2026-10-27 from a time element", meta.Published, meta.PublishedFrom)
	}

	modifiedHTML := `<html><head>
  <meta property="article:modified_time" content="2026-11-01T10:00:00Z">
</head><body><p>Text</p></body></html>`
	if meta := extractPageMeta([]byte(modifiedHTML)); !meta.Published.IsZero() {
		t.Fatalf("modified date = %v, want no publication date", meta.Published)
	}
}

// TestConvertFindsTheDateUnderTheTitle: when the head declares nothing, the date under the title is
// the article's date — and the conversion names that layer, so the reader can tell it from metadata.
func TestConvertFindsTheDateUnderTheTitle(t *testing.T) {
	raw := `<html><head><title>Caching in der Praxis</title></head><body><article>
  <h1>Caching in der Praxis</h1>
  <p>27. Oktober 2026</p>
  <p>Ein Cache hält Kopien nah, damit Zugriffe schneller sind. Dieser Text ist lang genug, damit
  die Lesbarkeitsanalyse ihn als Artikel erkennt und nicht als Navigation oder Kartenraster.</p>
  <p>Noch ein Absatz mit genug Worten, damit die Extraktion den Hauptinhalt sicher erkennt und der
  Konverter ihn als Markdown zurückgibt.</p>
</article></body></html>`
	out, err := (ReadabilityConverter{}).Convert(t.Context(), []byte(raw), "https://example.com/caching")
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if out.PublishedAt.Format("2006-01-02") != "2026-10-27" {
		t.Fatalf("published = %v, want 2026-10-27 from the text", out.PublishedAt)
	}
	if out.PublishedFrom != "text" {
		t.Errorf("from = %q, want text", out.PublishedFrom)
	}
}
