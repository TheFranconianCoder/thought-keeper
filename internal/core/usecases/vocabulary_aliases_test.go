package usecases

import "testing"

// TestAliasesOntoShelfChainsToTheShelf: an absorbed name may only be a shortcut onto the target
// vocabulary. A chain that lands on a shelf name is followed; one that ends outside the shelf is
// dropped, so the catalog's budget path decides instead of re-filing the claim under a fine name.
func TestAliasesOntoShelfChainsToTheShelf(t *testing.T) {
	shelf := []string{"caching", "compilers"}
	aliases := map[string]string{
		"cache-strategies":       "caching",          // direct
		"cache-invalidation":     "cache-strategies", // one hop onto the shelf
		"compilers-and-runtimes": "runtime-design",   // target off the shelf: dropped
		"loop":                   "loop-chain",       // cycle: dropped
	}
	got := aliasesOntoShelf(aliases, shelf)
	if got["cache-strategies"] != "caching" {
		t.Errorf("direct alias = %q, want caching", got["cache-strategies"])
	}
	if got["cache-invalidation"] != "caching" {
		t.Errorf("chained alias = %q, want caching", got["cache-invalidation"])
	}
	if _, ok := got["compilers-and-runtimes"]; ok {
		t.Errorf("an alias outside the shelf survived: %q", got["compilers-and-runtimes"])
	}
	if _, ok := got["loop"]; ok {
		t.Errorf("a cyclic alias survived: %q", got["loop"])
	}
}

// TestAliasesOntoShelfWithoutAShelf: a vault that never built a shelf keeps the whole alias table —
// there is no target to land on.
func TestAliasesOntoShelfWithoutAShelf(t *testing.T) {
	aliases := map[string]string{"cache-strategies": "caching"}
	got := aliasesOntoShelf(aliases, nil)
	if got["cache-strategies"] != "caching" {
		t.Errorf("alias table = %v, want the whole table", got)
	}
}
