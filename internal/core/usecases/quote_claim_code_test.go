package usecases_test

import (
	"context"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
)

func TestQuoteClaimCodeRepairsBareBracketSyntax(t *testing.T) {
	ctx := context.Background()
	bracket := ulid.Make()
	codeOnly := ulid.Make()
	store := &fakeAtomStore{m: map[string]entities.Atom{}}
	for _, a := range []entities.Atom{
		{
			ID: bracket, Topic: "c-attributes", Claim: "An attribute-specifier is doubled.",
			Body: "The production is [[ attribute-list ]], so it is doubled.",
		},
		{
			ID: codeOnly, Topic: "c-attributes", Claim: "The attribute marks a function.",
			Body: "The declaration `[[unsequenced]]` is already code.",
		},
	} {
		a.Path = a.ID.String() + ".md"
		store.m[a.ID.String()] = a
	}
	repo := &fakeAtomRepo{saved: []entities.Atom{store.m[bracket.String()], store.m[codeOnly.String()]}}

	res, err := (usecases.QuoteClaimCode{Atoms: store, AtomRepo: repo, Clock: fixedClock{}}).Execute(ctx)
	if err != nil {
		t.Fatalf("quote claim code: %v", err)
	}
	if res.Scanned != 2 || res.Repaired != 1 || res.Fragments != 1 || res.Skipped != 0 {
		t.Fatalf("result = %+v, want 2 scanned, 1 repaired, 1 fragment", res)
	}
	got, err := repo.GetAtom(ctx, bracket)
	if err != nil {
		t.Fatalf("get repaired claim: %v", err)
	}
	if !strings.Contains(got.Body, "`[[ attribute-list ]]`") {
		t.Errorf("body = %q, want the bracket span in inline code", got.Body)
	}
	if !got.UpdatedAt.Equal(fixedClock{}.Now()) {
		t.Errorf("updated = %v, want the clock's time", got.UpdatedAt)
	}

	// A second pass finds the pair inside a code span and changes nothing.
	again, err := (usecases.QuoteClaimCode{Atoms: store, AtomRepo: repo, Clock: fixedClock{}}).Execute(ctx)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if again.Repaired != 0 || again.Fragments != 0 {
		t.Fatalf("second pass = %+v, want no change", again)
	}
}
