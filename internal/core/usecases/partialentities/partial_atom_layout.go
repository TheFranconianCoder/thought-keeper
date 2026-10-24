package partialentities

// PartialAtomLayout reports what a vault holds in the OLD claim layout — branch folders, where the
// branch was a directory and the file name was a slug — and what a reorganize did about it.
//
// The layout the app writes is the ID one (`atoms/<ab>/<cd>/<id>.md`, the way a source is addressed
// by its hash); this is the account of moving a vault from the other one.
type PartialAtomLayout struct {
	// Folders are the branch folders found, Claims how many claims they carry and Indexes how many
	// generated branch index files stand beside them.
	Folders []string
	Claims  int
	Indexes int
	// Moved is how many claims the reorganize wrote at their canonical path, Removed how many folders
	// it deleted once they were empty, IndexesRemoved how many generated branch indexes it deleted
	// (they catalog a folder, and there are no branch folders any more), and Skipped how many files it
	// could not read — such a file is never thrown away, it stays where it is.
	Moved          int
	Removed        int
	IndexesRemoved int
	Skipped        int
	// Usage is how many usage records have no atom beside them — the mark a write at a STALE path
	// leaves behind, because a record is written next to its atom. UsageRemoved is how many of them the
	// reorganize trashed, and DirsRemoved how many directories ended up empty and went with them.
	Usage        int
	UsageRemoved int
	DirsRemoved  int
	// Kept names the folders that still hold something after the move (an unreadable file, a
	// subdirectory): they are left alone, and said out loud.
	Kept []string
}
