package extract_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/tree"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/extract"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// TestEPUBChaptersAndMetadata: a book is read as a book. The spine's documents are the chapters (level
// 1, one page each), the document's own headings hang under them (level 2 and deeper), and the
// metadata the format declares — dc:title, dc:language — is reported instead of guessed.
func TestEPUBChaptersAndMetadata(t *testing.T) {
	data := buildEPUB(t, map[string]string{
		"META-INF/container.xml": `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`,
		"OEBPS/content.opf": `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>The Cache Book</dc:title>
    <dc:language>en-US</dc:language>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
    <item id="ch2" href="ch2.xhtml" media-type="application/xhtml+xml"/>
    <item id="cover" href="cover.png" media-type="image/png"/>
  </manifest>
  <spine><itemref idref="ch1"/><itemref idref="ch2"/></spine>
</package>`,
		"OEBPS/nav.xhtml": `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<body><nav epub:type="toc"><ol>
  <li><a href="ch1.xhtml">1. Caching Basics</a></li>
  <li><a href="ch2.xhtml#top">2. Invalidation</a></li>
</ol></nav></body></html>`,
		"OEBPS/ch1.xhtml": `<html><body>
<h1>Caching Basics</h1>
<p>A cache is safe only when the key covers every input.</p>
<h2>Keys</h2>
<p>The key is the identity of a cached value.</p>
<img src="fig1.png" alt="a figure"/>
</body></html>`,
		"OEBPS/ch2.xhtml": `<html><body>
<h2>Invalidation</h2>
<p>Invalidation is the hard part.</p>
<nav><a href="ch1.xhtml">back</a></nav>
</body></html>`,
	})

	var events []entities.ExtractEvent
	doc, err := extract.New(nil, extract.Config{}).Extract(context.Background(), ".epub", data, func(ev entities.ExtractEvent) {
		events = append(events, ev)
	})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if doc.Title != "The Cache Book" {
		t.Errorf("declared title = %q, want the dc:title", doc.Title)
	}
	if doc.Language != "en" {
		t.Errorf("declared language = %q, want en (from en-US)", doc.Language)
	}

	type want struct {
		level int
		title string
		page  int
		text  string
	}
	wants := []want{
		{1, "1. Caching Basics", 1, "A cache is safe only when the key covers every input."},
		{2, "Keys", 1, "The key is the identity of a cached value."},
		{1, "2. Invalidation", 2, "Invalidation is the hard part."},
	}
	if len(doc.Parts) != len(wants) {
		t.Fatalf("parts = %d, want %d: %+v", len(doc.Parts), len(wants), doc.Parts)
	}
	for i, w := range wants {
		got := doc.Parts[i]
		if got.Level != w.level || got.Title != w.title || got.Page != w.page {
			t.Errorf("part %d = level %d %q page %d, want level %d %q page %d",
				i, got.Level, got.Title, got.Page, w.level, w.title, w.page)
		}
		if !strings.Contains(got.Text, w.text) {
			t.Errorf("part %d text = %q, want it to contain %q", i, got.Text, w.text)
		}
	}

	// The figure and the in-document navigation are gone, and dropping them is reported.
	images := 0
	for _, ev := range events {
		if ev.Kind == "images" {
			images += ev.Count
		}
	}
	if images != 1 {
		t.Errorf("images reported = %d, want 1: %+v", images, events)
	}
	for _, p := range doc.Parts {
		if strings.Contains(p.Text, "back") || strings.Contains(p.Text, "figure") {
			t.Errorf("dropped content reached a part: %+v", p)
		}
	}

	// A chapter survives the rendering round trip: the outline reads pages and levels back, so a
	// rebuild produces the same tree the derivation was built on.
	reparsed := tree.ParseRendering(tree.Render(doc.Parts))
	if len(reparsed) != len(doc.Parts) {
		t.Fatalf("rendering round trip = %d parts, want %d", len(reparsed), len(doc.Parts))
	}
	for i := range reparsed {
		if reparsed[i].Level != doc.Parts[i].Level || reparsed[i].Page != doc.Parts[i].Page ||
			reparsed[i].Title != doc.Parts[i].Title {
			t.Errorf("round trip part %d = %+v, want %+v", i, reparsed[i], doc.Parts[i])
		}
	}

	// The outline of a book is a book: each chapter is a node, and its sections hang under it.
	nodes := tree.Build(reparsed)
	ids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.NodeID)
	}
	if got, want := strings.Join(ids, " "), "0 0.1 0.1.1 0.2"; got != want {
		t.Errorf("tree node ids = %q, want %q (chapters with their sections)", got, want)
	}
	if nodes[1].Title != "1. Caching Basics" || nodes[2].Title != "Keys" || nodes[3].Title != "2. Invalidation" {
		t.Errorf("tree titles = %q / %q / %q", nodes[1].Title, nodes[2].Title, nodes[3].Title)
	}
}

// TestEPUBChapterShapes: the chapter is the top of a book whatever its own headings are called — a
// document whose headings start at h1 and one whose headings start at h3 produce the same shape.
func TestEPUBChapterShapes(t *testing.T) {
	data := buildEPUB(t, map[string]string{
		"META-INF/container.xml": containerXML,
		"OEBPS/content.opf":      packageXML("A Book", "de", []string{"a.xhtml", "b.xhtml"}),
		"OEBPS/a.xhtml": `<html><body><h1>One</h1><p>first</p>
<h3>Deep</h3><p>nested</p></body></html>`,
		"OEBPS/b.xhtml": `<html><body><p>no heading at all</p></body></html>`,
	})
	doc, err := extract.New(nil, extract.Config{}).Extract(context.Background(), ".epub", data, nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(doc.Parts) != 3 {
		t.Fatalf("parts = %d, want 3: %+v", len(doc.Parts), doc.Parts)
	}
	// The chapter folds its own first heading, and the shallowest inner heading becomes level 2.
	if doc.Parts[0].Level != 1 || doc.Parts[0].Title != "One" || doc.Parts[0].Page != 1 {
		t.Errorf("chapter one = %+v", doc.Parts[0])
	}
	if doc.Parts[1].Level != 2 || doc.Parts[1].Title != "Deep" || doc.Parts[1].Page != 1 {
		t.Errorf("section = %+v, want level 2 on the same chapter", doc.Parts[1])
	}
	// A document without a heading is still a chapter of its own, with its text and its own page.
	if doc.Parts[2].Level != 1 || doc.Parts[2].Page != 2 || doc.Parts[2].Title != "Chapter 2" {
		t.Errorf("chapter two = %+v", doc.Parts[2])
	}
	if !strings.Contains(doc.Parts[2].Text, "no heading at all") {
		t.Errorf("chapter two text = %q", doc.Parts[2].Text)
	}
}

// TestEPUBEncryptedIsRefused: a DRM-protected book is a wall. It is reported with the reason, never
// read as an empty book.
func TestEPUBEncryptedIsRefused(t *testing.T) {
	data := buildEPUB(t, map[string]string{
		"META-INF/container.xml":  containerXML,
		"META-INF/encryption.xml": encryptionXML("OEBPS/a.xhtml", "http://www.w3.org/2001/04/xmlenc#aes128-cbc"),
		"OEBPS/content.opf":       packageXML("A Book", "en", []string{"a.xhtml"}),
		"OEBPS/a.xhtml":           `<html><body><p>text</p></body></html>`,
	})
	_, err := extract.New(nil, extract.Config{}).Extract(context.Background(), ".epub", data, nil)
	if !errors.Is(err, types.ErrUnsupportedSource) {
		t.Fatalf("err = %v, want ErrUnsupportedSource", err)
	}
	if !strings.Contains(err.Error(), "encrypted") {
		t.Errorf("err = %v, want it to say the book is encrypted", err)
	}
}

// TestEPUBFontObfuscationIsNotDRM: the standard lets a publisher scramble the embedded fonts while
// the text stays readable. That is not a wall.
func TestEPUBFontObfuscationIsNotDRM(t *testing.T) {
	data := buildEPUB(t, map[string]string{
		"META-INF/container.xml":  containerXML,
		"META-INF/encryption.xml": encryptionXML("OEBPS/fonts/serif.ttf", "http://www.idpf.org/2008/embedding"),
		"OEBPS/content.opf":       packageXML("A Book", "en", []string{"a.xhtml"}),
		"OEBPS/a.xhtml":           `<html><body><h1>One</h1><p>readable text</p></body></html>`,
	})
	doc, err := extract.New(nil, extract.Config{}).Extract(context.Background(), ".epub", data, nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(doc.Parts) == 0 {
		t.Fatal("no parts: an obfuscated font is not encrypted content")
	}
}

// TestEPUBWithoutTextIsRefused: a fixed-layout or image-only book has nothing to read. It is flagged,
// never silently empty.
func TestEPUBWithoutTextIsRefused(t *testing.T) {
	data := buildEPUB(t, map[string]string{
		"META-INF/container.xml": containerXML,
		"OEBPS/content.opf":      packageXML("A Comic", "en", []string{"a.xhtml"}),
		"OEBPS/a.xhtml":          `<html><body><img src="page1.png"/></body></html>`,
	})
	_, err := extract.New(nil, extract.Config{}).Extract(context.Background(), ".epub", data, nil)
	if !errors.Is(err, types.ErrUnsupportedSource) {
		t.Fatalf("err = %v, want ErrUnsupportedSource", err)
	}
	if !strings.Contains(err.Error(), "no readable text") {
		t.Errorf("err = %v, want it to say there is no readable text", err)
	}
}

// TestEPUBWithoutContainerIsRefused: a ZIP that is not an EPUB is not guessed at.
func TestEPUBWithoutContainerIsRefused(t *testing.T) {
	data := buildEPUB(t, map[string]string{"readme.txt": "not a book"})
	_, err := extract.New(nil, extract.Config{}).Extract(context.Background(), ".epub", data, nil)
	if !errors.Is(err, types.ErrUnsupportedSource) {
		t.Fatalf("err = %v, want ErrUnsupportedSource", err)
	}
}

const containerXML = `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`

// packageXML builds a minimal EPUB 3 package document with one manifest item and one spine entry per
// chapter file.
func packageXML(title, language string, chapters []string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>` + title + `</dc:title>
    <dc:language>` + language + `</dc:language>
  </metadata>
  <manifest>
`)
	for i, href := range chapters {
		b.WriteString(`    <item id="c` + itoa(i) + `" href="` + href + `" media-type="application/xhtml+xml"/>` + "\n")
	}
	b.WriteString("  </manifest>\n  <spine>\n")
	for i := range chapters {
		b.WriteString(`    <itemref idref="c` + itoa(i) + `"/>` + "\n")
	}
	b.WriteString("  </spine>\n</package>")
	return b.String()
}

func encryptionXML(uri, algorithm string) string {
	return `<?xml version="1.0"?>
<encryption xmlns="urn:oasis:names:tc:opendocument:xmlns:container" xmlns:enc="http://www.w3.org/2001/04/xmlenc#">
  <enc:EncryptedData>
    <enc:EncryptionMethod Algorithm="` + algorithm + `"/>
    <enc:CipherData><enc:CipherReference URI="` + uri + `"/></enc:CipherData>
  </enc:EncryptedData>
</encryption>`
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

// buildEPUB writes the files into a ZIP in a deterministic order, so a failure is reproducible.
func buildEPUB(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range slices.Sorted(maps.Keys(files)) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(files[name])); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}
