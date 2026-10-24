package claims

import "testing"

func TestStructuralTopicsAreRejected(t *testing.T) {
	for _, topic := range []string{"chapter-11", "chapter-1", "appendix-a", "appendixes", "book-structure", "book-audience", "rust-book", "table-of-contents", "part-2"} {
		if !TopicIsStructural(topic) {
			t.Errorf("topic %q should be structural", topic)
		}
	}
	for _, topic := range []string{"caching", "coverage-profiling", "page-faults", "source-code", "document-databases", "rust-ownership"} {
		if TopicIsStructural(topic) {
			t.Errorf("topic %q is a subject, not document structure", topic)
		}
	}
}

func TestSourceRelativeClaimsAreRejected(t *testing.T) {
	cases := []string{
		"Chapter 11 is all about testing.",
		"Chapter 4 teaches Rust's ownership system.",
		"The book assumes the reader reads it in sequence.",
		"This section only covers attaching a debugger.",
		"Later chapters build on concepts introduced in earlier chapters.",
		"Information about Rust editions is provided in Appendix E.",
		"The author explains the feature in the next section.",
	}
	for _, claim := range cases {
		if Standalone("testing", claim, "Chapter 11 is devoted to testing.") {
			t.Errorf("claim %q should be rejected", claim)
		}
	}
}

func TestStandaloneClaimPasses(t *testing.T) {
	if !Standalone("rust-ownership",
		"Rust's ownership system makes memory safety a compile-time property.",
		"Each value has one owner; the value is dropped when that owner goes out of scope.") {
		t.Fatal("a self-contained claim was rejected")
	}
	if !Standalone("coverage-profiling",
		"Coverage profiling records which statements a test run executed.",
		"`go tool covdata` turns the binary coverage data into a readable report.") {
		t.Fatal("a self-contained technical claim was rejected")
	}
}
