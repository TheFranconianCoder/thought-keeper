package entities

import "time"

// ReaderState is the reader's view of the vault's arrivals: the instant up to which they have been
// seen. It is ONE record per vault, not per client. Whichever client becomes active first
// acknowledges the batch for every client, so the same sources are not new on every device.
type ReaderState struct {
	SeenAt time.Time
}

// ReaderActivation is the answer to a client that became active. Since is the watermark before the
// acknowledgment — the baseline its "new" marks use — and SeenAt is the watermark after it. First is
// true when the vault had no watermark yet: the client shows no marks and the record is initialized.
// Changed reports whether the acknowledgment moved the watermark, so the caller knows whether there
// is something to announce.
type ReaderActivation struct {
	Since   time.Time
	SeenAt  time.Time
	First   bool
	Changed bool
}
