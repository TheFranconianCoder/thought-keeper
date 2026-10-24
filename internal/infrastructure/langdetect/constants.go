package langdetect

const (
	maxTrigramDistance = 300
	maxTotalDistance   = 90000
)

// ReliableConfidenceThreshold is confidence rating that has to be succeeded
// for the language detection to be considered reliable.
const ReliableConfidenceThreshold = 0.8
