package gateways

import (
	"context"
	"strings"
	"testing"
)

// TestConvertKeepsTables is the one regression the visible Markdown had: the package default
// flattened a table into a single line, so a benchmark table or a comparison lost its columns and
// every claim distilled from it lost the row it belonged to.
func TestConvertKeepsTables(t *testing.T) {
	const page = `<!doctype html><html><head><title>Benchmarks</title></head><body><article>
<h1>Benchmarks</h1>
<p>Requesting 933 real web pages over the network.</p>
<table>
  <thead><tr><th>Metric</th><th>Lightpanda</th><th>Headless Chrome</th></tr></thead>
  <tbody>
    <tr><td>Memory (peak)</td><td>123MB</td><td>2GB</td></tr>
    <tr><td>Execution time</td><td>5s</td><td>46s</td></tr>
  </tbody>
</table>
</article></body></html>`

	out, err := ReadabilityConverter{}.Convert(context.Background(), []byte(page), "https://example.com/benchmarks")
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	// The plugin pads the cells so the source stays readable; the SHAPE is what matters here.
	compact := strings.Join(strings.Fields(out.Markdown), " ")
	for _, want := range []string{
		"| Metric | Lightpanda | Headless Chrome |",
		"| Memory (peak) | 123MB | 2GB |",
		"| Execution time | 5s | 46s |",
	} {
		if !strings.Contains(compact, want) {
			t.Errorf("table row missing from the Markdown:\nwant: %s\n--- got ---\n%s", want, compact)
		}
	}
	if strings.Contains(out.Markdown, "Metric Lightpanda Headless Chrome") {
		t.Errorf("the table was flattened:\n%s", out.Markdown)
	}
}
