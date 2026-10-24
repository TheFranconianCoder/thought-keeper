package entities

// SourceNode is one node of a source's tree index (PageIndex shape).
type SourceNode struct {
	NodeID    string // stable address, e.g. "0.2.1"
	ParentID  string // "" for the root
	Depth     int
	Ord       int // global pre-order index = document order
	Title     string
	Summary   string
	StartPage int
	EndPage   int
	// Text is the node's text slice. It is NEVER stored in the outline: the outline keeps the
	// REFERENCE (TextStart/TextEnd into the source's readable text) plus the hash, and the text is
	// resolved from the reference when an outline is read. It is a materialised view, not a copy.
	Text string
	// TextStart/TextEnd are the node's text as a byte range in the source's readable text, and
	// TextHash is the hash of that slice — what the index stores and what tells a rebuilt tree apart
	// from the one it replaces, without comparing whole documents.
	TextStart int
	TextEnd   int
	TextHash  string
	OCR       bool
	// HasText says whether this node carries text of its own. It is the index's answer (the DB keeps
	// a text HASH, never the text): a node without text is a grouping heading — the document's title,
	// a part — and the outline shows it as one instead of offering it as something to read. A node
	// read from the SIDECAR has its text right here, which is the same fact.
	HasText bool
}
