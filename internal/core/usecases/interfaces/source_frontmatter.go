package interfaces

import "context"

// SourceFrontmatter reads what a source's own object declares about itself — its YAML header.
//
// It is a port because YAML is decoded in the infrastructure only: the core may ask "what URL does
// this snapshot name?", never how the answer is encoded.
type SourceFrontmatter interface {
	// DeclaredURL returns the address a source's object declares, or "" when it declares none.
	DeclaredURL(ctx context.Context, hash, ext string) (string, error)
}
