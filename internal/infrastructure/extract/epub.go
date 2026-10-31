package extract

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"golang.org/x/net/html"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// EPUB reading is pure Go on purpose: an EPUB is a ZIP of XHTML, and the standard library plus the
// converter the URL path already uses can read it. There is no external tool to install, so — unlike
// a PDF — nothing can be "missing" on the machine, and the step never depends on the host.
//
// A book's structure is its SPINE: the ordered list of content documents the package declares. Each
// spine document is one CHAPTER, and its own headings hang under it, so the outline of a book is a
// book (chapters, then sections) instead of a flat run of headings.
const (
	// maxEPUBEntryBytes caps ONE decompressed entry. A chapter of a book is kilobytes; anything near
	// this is not a chapter, and a ZIP header can claim any size it likes.
	maxEPUBEntryBytes = 32 << 20
	// maxEPUBTotalBytes caps everything the extraction decompresses, which is what a ZIP bomb
	// multiplies.
	maxEPUBTotalBytes = 256 << 20
)

// epubSections reads an EPUB into parts: one level-1 part per chapter (spine document), its headings
// as the parts under it. Images are dropped — this vault reads text — and the count is reported so a
// book that is mostly figures does not look like a book that is mostly prose.
func (e *Extractor) epubSections(data []byte, onEvent func(entities.ExtractEvent)) (entities.Extraction, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return entities.Extraction{}, types.New("unsupported_source",
			"the file is not a readable EPUB (a ZIP container): "+err.Error(), types.ErrUnsupportedSource)
	}
	files := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		files[epubName(f.Name)] = f
	}

	budget := maxEPUBTotalBytes
	opfPath, err := epubPackagePath(files, &budget)
	if err != nil {
		return entities.Extraction{}, err
	}
	raw, err := readEPUBEntry(files, opfPath, &budget)
	if err != nil {
		return entities.Extraction{}, err
	}
	if len(raw) == 0 {
		return entities.Extraction{}, types.New("unsupported_source",
			"the EPUB's package document is missing ("+opfPath+")", types.ErrUnsupportedSource)
	}
	var pkg epubPackage
	if err := xml.Unmarshal(raw, &pkg); err != nil {
		return entities.Extraction{}, types.New("unsupported_source",
			"the EPUB's package document could not be read: "+err.Error(), types.ErrUnsupportedSource)
	}
	if err := epubCheckEncrypted(files, &budget); err != nil {
		return entities.Extraction{}, err
	}

	out := entities.Extraction{
		Title:    firstText(pkg.Metadata.Titles),
		Language: normaliseEPUBLanguage(firstText(pkg.Metadata.Languages)),
	}

	items := make(map[string]epubItem, len(pkg.Manifest.Items))
	for _, item := range pkg.Manifest.Items {
		items[item.ID] = item
	}
	opfDir := path.Dir(opfPath)
	toc := epubTOC(files, &budget, opfDir, pkg, items)

	var parts []entities.Part
	chapters, images, skipped := 0, 0, 0
	for _, ref := range pkg.Spine.ItemRefs {
		item, ok := items[ref.IDRef]
		if !ok || !isEPUBContent(item.MediaType, item.Href) {
			continue
		}
		href := epubResolve(opfDir, item.Href)
		chapterRaw, err := readEPUBEntry(files, href, &budget)
		if err != nil {
			return entities.Extraction{}, err
		}
		if len(chapterRaw) == 0 {
			continue
		}
		markdown, chapterImages, err := epubMarkdown(chapterRaw)
		if err != nil {
			// One unreadable document must not lose the book: it is reported and skipped, never
			// silently turned into an empty chapter.
			skipped++
			if e.cfg.Log != nil {
				e.cfg.Log.Warn("epub: chapter skipped", "file", href, "err", err.Error())
			}
			if onEvent != nil {
				onEvent(entities.ExtractEvent{Kind: "skipped", Page: chapters + 1, Err: err})
			}
			continue
		}
		images += chapterImages
		chapterParts := epubChapterParts(markdown, chapters+1, toc[href])
		if len(chapterParts) == 0 {
			continue
		}
		chapters++
		parts = append(parts, chapterParts...)
	}

	if len(parts) == 0 || epubTextChars(parts) == 0 {
		return entities.Extraction{}, types.New("unsupported_source",
			"the EPUB has no readable text (a fixed-layout or image-only book)", types.ErrUnsupportedSource)
	}
	if images > 0 && onEvent != nil {
		onEvent(entities.ExtractEvent{Kind: "images", Count: images})
	}
	if e.cfg.Log != nil {
		e.cfg.Log.Info("epub read", "chapters", chapters, "parts", len(parts), "images", images, "skipped", skipped)
	}
	out.Parts = parts
	return out, nil
}

// epubChapterParts shapes one spine document into the parts of one chapter. The chapter itself is
// level 1 (the TOC's label, or the document's first heading); everything under it is shifted so the
// shallowest heading becomes level 2 — a document whose own headings start at h1 or h2 produces the
// same shape, which is what makes chapters comparable across books.
func epubChapterParts(markdown string, chapter int, tocTitle string) []entities.Part {
	sections := splitMarkdown(markdown)
	title := strings.TrimSpace(tocTitle)
	if title == "" && len(sections) > 0 {
		title = strings.TrimSpace(sections[0].Title)
	}
	if title == "" {
		title = fmt.Sprintf("Chapter %d", chapter)
	}

	chapterText := ""
	if len(sections) > 0 {
		// The opening section IS the chapter when it repeats the chapter's title (the usual
		// `<h1>Chapter 1</h1>`), or when it is text before any heading. Its text belongs to the
		// chapter, not to a child part that would say the same thing twice.
		first := sections[0]
		if first.Level == 0 || sameChapterTitle(first.Title, title) {
			chapterText = first.Text
			sections = sections[1:]
		}
	}

	parts := []entities.Part{{Level: 1, Title: title, Text: chapterText, Page: chapter}}
	base := 0
	for _, s := range sections {
		if s.Level > 0 && (base == 0 || s.Level < base) {
			base = s.Level
		}
	}
	if base == 0 {
		base = 1
	}
	for _, s := range sections {
		if strings.TrimSpace(s.Text) == "" && strings.TrimSpace(s.Title) == "" {
			continue
		}
		level := 2
		if s.Level > base {
			level = 2 + (s.Level - base)
		}
		if level > 6 {
			level = 6
		}
		parts = append(parts, entities.Part{Level: level, Title: s.Title, Text: s.Text, Page: chapter})
	}
	return parts
}

// sameChapterTitle reports whether a document's first heading names the same chapter the TOC does.
// Books number their chapters in the TOC ("1. Caching Basics") and often drop the number in the
// document's own heading ("Caching Basics"), so the comparison ignores a leading number and
// punctuation — otherwise the chapter would open with a section that repeats its own name.
func sameChapterTitle(heading, title string) bool {
	a, b := normaliseChapterTitle(heading), normaliseChapterTitle(title)
	return a != "" && a == b
}

func normaliseChapterTitle(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimLeft(s, "0123456789 .-–—:·")
	return strings.Join(strings.Fields(s), " ")
}

// epubMarkdown turns one spine document into Markdown: navigation, scripts, media and figures are
// dropped (the last are counted), and the rest is converted. Dropping them is not silent — the caller
// writes the image count into the source's log.
func epubMarkdown(raw []byte) (string, int, error) {
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return "", 0, err
	}
	images := 0
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for child := n.FirstChild; child != nil; {
			next := child.NextSibling
			if child.Type == html.ElementNode {
				switch child.Data {
				case "nav", "svg", "audio", "video", "iframe", "object", "canvas":
					n.RemoveChild(child)
					child = next
					continue
				case "img", "image":
					images++
					n.RemoveChild(child)
					child = next
					continue
				}
			}
			walk(child)
			child = next
		}
	}
	walk(doc)
	markdown, err := htmltomarkdown.ConvertNode(doc)
	if err != nil {
		return "", images, err
	}
	return string(markdown), images, nil
}

// epubPackagePath reads META-INF/container.xml and returns the path of the package document (the OPF
// that holds the metadata, the manifest and the spine).
func epubPackagePath(files map[string]*zip.File, budget *int) (string, error) {
	raw, err := readEPUBEntry(files, "META-INF/container.xml", budget)
	if err != nil {
		return "", err
	}
	if len(raw) == 0 {
		return "", types.New("unsupported_source",
			"the file is not a readable EPUB: META-INF/container.xml is missing", types.ErrUnsupportedSource)
	}
	var container epubContainer
	if err := xml.Unmarshal(raw, &container); err != nil {
		return "", types.New("unsupported_source",
			"the EPUB's container.xml could not be read: "+err.Error(), types.ErrUnsupportedSource)
	}
	for _, root := range container.Rootfiles {
		if name := strings.TrimSpace(root.FullPath); name != "" {
			return epubName(name), nil
		}
	}
	return "", types.New("unsupported_source",
		"the EPUB declares no package document", types.ErrUnsupportedSource)
}

// epubCheckEncrypted refuses a book whose CONTENT is encrypted. Font obfuscation is not DRM: the
// standard lets a publisher scramble the embedded fonts while the text stays readable, so only an
// encrypted document that is not a font is a wall.
func epubCheckEncrypted(files map[string]*zip.File, budget *int) error {
	raw, err := readEPUBEntry(files, "META-INF/encryption.xml", budget)
	if err != nil || len(raw) == 0 {
		return err
	}
	var enc epubEncryption
	if err := xml.Unmarshal(raw, &enc); err != nil {
		return types.New("unsupported_source",
			"the EPUB's encryption record could not be read: "+err.Error(), types.ErrUnsupportedSource)
	}
	for _, data := range enc.Data {
		uri := strings.TrimSpace(data.Cipher.URI)
		if uri == "" {
			continue
		}
		switch strings.ToLower(path.Ext(uri)) {
		case ".ttf", ".otf", ".woff", ".woff2":
			continue
		}
		return types.New("unsupported_source",
			"the EPUB is encrypted (DRM): its text cannot be read", types.ErrUnsupportedSource)
	}
	return nil
}

// epubTOC reads the book's own table of contents: EPUB 3's navigation document (a manifest item with
// the `nav` property) first, EPUB 2's NCX second. The labels are what a chapter is called when the
// document itself does not say.
func epubTOC(files map[string]*zip.File, budget *int, opfDir string, pkg epubPackage, items map[string]epubItem) map[string]string {
	for _, item := range pkg.Manifest.Items {
		if hasEPUBProperty(item.Properties, "nav") {
			if toc := epubNavTOC(files, budget, epubResolve(opfDir, item.Href)); len(toc) > 0 {
				return toc
			}
		}
	}
	if pkg.Spine.TOC != "" {
		if item, ok := items[pkg.Spine.TOC]; ok {
			return epubNCXTOC(files, budget, epubResolve(opfDir, item.Href))
		}
	}
	return nil
}

func epubNavTOC(files map[string]*zip.File, budget *int, href string) map[string]string {
	raw, err := readEPUBEntry(files, href, budget)
	if err != nil || len(raw) == 0 {
		return nil
	}
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return nil
	}
	toc := map[string]string{}
	var walk func(*html.Node) bool
	walk = func(n *html.Node) bool {
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if child.Type == html.ElementNode && child.Data == "nav" && isTOCNav(child) {
				collectEPUBLinks(child, path.Dir(href), toc)
				return true
			}
			if walk(child) {
				return true
			}
		}
		return false
	}
	walk(doc)
	return toc
}

// isTOCNav reports whether a <nav> is the table of contents. EPUB 3 spells it `epub:type="toc"`; a
// book that omits the type still has exactly one nav, so the first one is used.
func isTOCNav(n *html.Node) bool {
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, "epub:type") || strings.EqualFold(attr.Key, "role") {
			if strings.Contains(strings.ToLower(attr.Val), "toc") || strings.Contains(strings.ToLower(attr.Val), "doc-toc") {
				return true
			}
		}
	}
	return false
}

func collectEPUBLinks(n *html.Node, base string, toc map[string]string) {
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.Data == "a" {
			href := ""
			for _, attr := range child.Attr {
				if attr.Key == "href" {
					href = attr.Val
				}
			}
			if href != "" {
				if target := epubResolve(base, href); target != "" {
					if _, seen := toc[target]; !seen {
						if label := strings.TrimSpace(textOf(child)); label != "" {
							toc[target] = label
						}
					}
				}
			}
		}
		collectEPUBLinks(child, base, toc)
	}
}

func epubNCXTOC(files map[string]*zip.File, budget *int, href string) map[string]string {
	raw, err := readEPUBEntry(files, href, budget)
	if err != nil || len(raw) == 0 {
		return nil
	}
	var ncx epubNCX
	if err := xml.Unmarshal(raw, &ncx); err != nil {
		return nil
	}
	toc := map[string]string{}
	base := path.Dir(href)
	var collect func(points []epubNavPoint)
	collect = func(points []epubNavPoint) {
		for _, point := range points {
			label := strings.TrimSpace(point.Label.Text)
			if target := epubResolve(base, point.Content.Src); target != "" && label != "" {
				if _, seen := toc[target]; !seen {
					toc[target] = label
				}
			}
			collect(point.Points)
		}
	}
	collect(ncx.NavMap.Points)
	return toc
}

// readEPUBEntry reads one entry of the container. A missing entry is not an error — the caller
// decides whether it was required — and the budget is what keeps a ZIP bomb from being decompressed.
func readEPUBEntry(files map[string]*zip.File, name string, budget *int) ([]byte, error) {
	f, ok := files[epubName(name)]
	if !ok {
		return nil, nil
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("epub: open %s: %w", name, err)
	}
	defer func() { _ = rc.Close() }()

	limit := int64(maxEPUBEntryBytes)
	if remaining := int64(*budget); remaining < limit {
		limit = remaining
	}
	if limit <= 0 {
		return nil, epubTooLarge()
	}
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, fmt.Errorf("epub: read %s: %w", name, err)
	}
	if int64(len(data)) > limit {
		return nil, epubTooLarge()
	}
	*budget -= len(data)
	return data, nil
}

// epubTextChars counts the text the book actually carries. A book whose every chapter is a figure
// (a comic, a fixed-layout picture book) has titles but nothing to read, and the step must say so
// instead of recording an empty capture.
func epubTextChars(parts []entities.Part) int {
	n := 0
	for _, p := range parts {
		n += len(strings.TrimSpace(p.Text))
	}
	return n
}

func epubTooLarge() error {
	return types.New("unsupported_source",
		fmt.Sprintf("the EPUB expands to more than %d MiB of text: it is not read as a book", maxEPUBTotalBytes>>20),
		types.ErrUnsupportedSource)
}

// epubResolve resolves a manifest or TOC href against the directory that declares it, without the
// fragment: a chapter is addressed by its document, and two TOC entries that differ only in their
// fragment are the same chapter.
func epubResolve(base, href string) string {
	href = strings.TrimSpace(href)
	if i := strings.IndexByte(href, '#'); i >= 0 {
		href = href[:i]
	}
	if href == "" {
		return ""
	}
	if unescaped, err := url.PathUnescape(href); err == nil {
		href = unescaped
	}
	return epubName(path.Join(base, href))
}

// epubName is the form in which an EPUB entry is looked up: forward slashes, no leading slash or
// "./", cleaned. ZIP names are the only names that exist — nothing is ever written to disk.
func epubName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimPrefix(name, "./")
	name = strings.TrimPrefix(name, "/")
	if name == "" {
		return ""
	}
	return path.Clean(name)
}

// isEPUBContent reports whether a manifest item is a document the spine can order. The spine names
// content documents; a cover image or a font that ended up in the spine is not a chapter. A book that
// forgot the media type is still read by its extension — refusing it would lose a whole book to one
// missing attribute.
func isEPUBContent(mediaType, href string) bool {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "application/xhtml+xml", "text/html", "application/x-dtbook+xml":
		return true
	case "":
		switch strings.ToLower(path.Ext(href)) {
		case ".xhtml", ".html", ".htm":
			return true
		}
	}
	return false
}

func hasEPUBProperty(properties, want string) bool {
	for _, p := range strings.Fields(properties) {
		if strings.EqualFold(p, want) {
			return true
		}
	}
	return false
}

// firstText returns the first non-empty value, trimmed.
func firstText(values []string) string {
	for _, v := range values {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}

// normaliseEPUBLanguage turns a declared language ("en-US", "de", "eng") into the ISO 639-1 hint the
// vault stores (ISO 639-3 only when no 639-1 exists). Anything else is no hint at all.
func normaliseEPUBLanguage(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if i := strings.IndexAny(raw, "-_"); i > 0 {
		raw = raw[:i]
	}
	if len(raw) != 2 && len(raw) != 3 {
		return ""
	}
	for _, r := range raw {
		if r < 'a' || r > 'z' {
			return ""
		}
	}
	return raw
}

// textOf returns the text content of a node, trimmed.
func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

// The XML shapes the container and the package document have. Names are matched by local name (the
// namespaces are the formats' own business), and only the fields this reader uses are declared.
type epubContainer struct {
	Rootfiles []struct {
		FullPath  string `xml:"full-path,attr"`
		MediaType string `xml:"media-type,attr"`
	} `xml:"rootfiles>rootfile"`
}

type epubPackage struct {
	Metadata struct {
		Titles    []string `xml:"title"`
		Languages []string `xml:"language"`
	} `xml:"metadata"`
	Manifest struct {
		Items []epubItem `xml:"item"`
	} `xml:"manifest"`
	Spine struct {
		TOC      string        `xml:"toc,attr"`
		ItemRefs []epubItemRef `xml:"itemref"`
	} `xml:"spine"`
}

type epubItem struct {
	ID         string `xml:"id,attr"`
	Href       string `xml:"href,attr"`
	MediaType  string `xml:"media-type,attr"`
	Properties string `xml:"properties,attr"`
}

type epubItemRef struct {
	IDRef string `xml:"idref,attr"`
}

type epubEncryption struct {
	Data []struct {
		Cipher struct {
			URI string `xml:"URI,attr"`
		} `xml:"CipherData>CipherReference"`
	} `xml:"EncryptedData"`
}

type epubNCX struct {
	NavMap struct {
		Points []epubNavPoint `xml:"navPoint"`
	} `xml:"navMap"`
}

type epubNavPoint struct {
	Label struct {
		Text string `xml:"text"`
	} `xml:"navLabel"`
	Content struct {
		Src string `xml:"src,attr"`
	} `xml:"content"`
	Points []epubNavPoint `xml:"navPoint"`
}
