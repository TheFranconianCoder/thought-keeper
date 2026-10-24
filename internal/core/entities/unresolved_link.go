package entities

import "github.com/oklog/ulid/v2"

// UnresolvedLink is a wikilink whose target does not exist (a "red link").
type UnresolvedLink struct {
	FromID    ulid.ULID
	FromTitle string
	RawTarget string
}
