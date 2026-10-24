package markdown

import "gopkg.in/yaml.v3"

// SourceDeclaredURL reads the URL a source's own object declares in its YAML header.
//
// A URL snapshot is stored as a plain Markdown file whose frontmatter names the address it came from
// (url:, fetched_at:, http_status:, title:). That header is the vault's own record of where the
// snapshot came from — which is what makes an old snapshot recoverable when the INDEX has lost the
// fact that it was a URL at all.
//
// It lives here because YAML is decoded in the infrastructure only (see ARCHITECTURE.md § 2.2).
func SourceDeclaredURL(text string) string {
	front, _ := splitFrontmatter(text)
	if front == nil {
		return ""
	}
	var meta struct {
		URL string `yaml:"url"`
	}
	if err := yaml.Unmarshal([]byte(*front), &meta); err != nil {
		return ""
	}
	return meta.URL
}
