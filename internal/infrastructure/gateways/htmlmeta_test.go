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
