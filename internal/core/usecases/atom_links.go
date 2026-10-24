package usecases

import "context"

// AtomLinkResolver resolves the raw `[[Claim Text]]` targets of atom links to the atom id they
// name. It is optional: a test fake or a small tool path may not have one, and a run that cannot
// resolve still writes honest raw links (they stay red until a resolver runs).
type AtomLinkResolver interface {
	ResolveLinks(ctx context.Context) (int64, error)
}
