package partialentities

// PartialAtomDuplicates reports claims that are the SAME claim written more than once: same branch,
// same statement, same provenance, same body, type, status and tags. What differs is only the ID — so
// the copies are indistinguishable, and keeping one of them loses nothing.
//
// It exists because a failed write used to leave its file behind: a model that retries a write it
// cannot see leaves one file per attempt, and after a reindex those copies are real claims. The app
// reports them and offers to collapse them — it never decides that on its own.
type PartialAtomDuplicates struct {
	// Groups is how many claims exist more than once, Files how many files those groups hold (the
	// copies included), and Collapsed how many files a collapse moved to the trash.
	Groups    int
	Files     int
	Collapsed int
	// Example is one statement that exists more than once — the report should name something a reader
	// can recognise, not only count.
	Example string
}
