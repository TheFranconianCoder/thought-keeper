package payloads

// Node is the wire shape of one source-tree node.
type Node struct {
	NodeID    string `json:"node_id"`
	ParentID  string `json:"parent_id"`
	Depth     int    `json:"depth"`
	Ord       int    `json:"ord"`
	Title     string `json:"title"`
	Summary   string `json:"summary,omitempty"`
	StartPage int    `json:"start_page,omitempty"`
	EndPage   int    `json:"end_page,omitempty"`
	OCR       bool   `json:"ocr,omitempty"`
	Text      string `json:"text,omitempty"`
	// HasText marks a part with text of its own: the outline offers those to read and shows the
	// others as the grouping headings they are.
	HasText bool `json:"has_text,omitempty"`
}

// Tree is the response of GET /api/v1/sources/{hash}/tree.
type Tree struct {
	Hash  string `json:"hash"`
	Nodes []Node `json:"nodes"`
	Count int    `json:"count"`
}

// RebuildTreesResult is the response of POST /api/v1/rebuild-trees.
type RebuildTreesResult struct {
	Sources int `json:"sources"`
	Built   int `json:"built"`
	Failed  int `json:"failed"`
}

// TreeRebuilt reports one rebuilt tree.
type TreeRebuilt struct {
	Hash  string `json:"hash"`
	Nodes int    `json:"nodes"`
	Pages int    `json:"pages"`
	Title string `json:"title"`
	OCR   bool   `json:"ocr"`
}
