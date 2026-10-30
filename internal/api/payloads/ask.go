package payloads

// AskSummary is one saved Ask in the list (the answer is read when it is opened).
type AskSummary struct {
	ID        string   `json:"id"`
	Question  string   `json:"question"`
	Tags      []string `json:"tags,omitempty"`
	Source    string   `json:"source,omitempty"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
}

// Ask is one saved question and its answer.
type Ask struct {
	ID        string   `json:"id"`
	Question  string   `json:"question"`
	Answer    string   `json:"answer"`
	Tags      []string `json:"tags,omitempty"`
	Source    string   `json:"source,omitempty"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
}

// AskList is the response of GET /api/v1/asks.
type AskList struct {
	Asks  []AskSummary `json:"asks"`
	Count int          `json:"count"`
}
