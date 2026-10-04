package store

import (
	"context"
	"errors"
	"testing"
)

// TestAParentMustExistAndNeverLoop: a local branch belongs to its federal
// union, and a chain that came back round would have no top to show.
func TestAParentMustExistAndNeverLoop(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	federal := organisation(t, s, "Beispiel-Gewerkschaft", "")
	branch := organisation(t, s, "Beispiel-Gewerkschaft Köln", federal.ID)

	// A new organisation has no identifier yet, and is in no chain: the
	// empty parent at the top of its parent's must not read as itself.
	if err := s.CheckParent(ctx, "", branch.ID); err != nil {
		t.Errorf("a new organisation under an existing chain: %v", err)
	}
	if err := s.CheckParent(ctx, "", "no-such-organisation"); !errors.Is(err, ErrParentNotFound) {
		t.Errorf("a parent that does not exist: %v, want ErrParentNotFound", err)
	}
	if err := s.CheckParent(ctx, federal.ID, federal.ID); !errors.Is(err, ErrParentLoop) {
		t.Errorf("its own parent: %v, want ErrParentLoop", err)
	}
	if err := s.CheckParent(ctx, federal.ID, branch.ID); !errors.Is(err, ErrParentLoop) {
		t.Errorf("under its own branch: %v, want ErrParentLoop", err)
	}
	if err := s.CheckParent(ctx, branch.ID, ""); err != nil {
		t.Errorf("no parent at all: %v", err)
	}
}
