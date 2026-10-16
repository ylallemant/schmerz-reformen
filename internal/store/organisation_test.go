package store

import (
	"context"
	"errors"
	"testing"

	"github.com/ylallemant/schmerz-reformen/internal/geo"
	"github.com/ylallemant/schmerz-reformen/internal/models"
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

// TestTheMapShowsOrganisationsSomebodyPublishedListsAndCountsThemAll: an
// organisation has no page of its own, so it is public only as a published
// collective's member — and like every layer, the viewport narrows what is
// drawn and never what is counted.
func TestTheMapShowsOrganisationsSomebodyPublishedListsAndCountsThemAll(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	placed := func(name string, lat, lng float64) models.Organisation {
		made := organisation(t, s, name, "")
		made.Location = models.Location{Latitude: lat, Longitude: lng, Label: name}
		if err := s.SaveOrganisation(ctx, &made); err != nil {
			t.Fatal(err)
		}
		return made
	}
	member := func(collectiveID, organisationID string) {
		if err := s.SaveMember(ctx, &models.CollectiveMember{CollectiveID: collectiveID, OrganisationID: organisationID}); err != nil {
			t.Fatal(err)
		}
	}

	published := collective(t, s, "duesseldorf")
	draft := collective(t, s, "geheim")
	draft.Status = models.StatusDraft
	if err := s.SaveCollective(ctx, &draft); err != nil {
		t.Fatal(err)
	}

	verdi := placed("ver.di Düsseldorf", 51.2277, 6.7735)
	linke := placed("Die Linke Köln", 50.9375, 6.9603)
	hidden := placed("Nur im Entwurf", 51.23, 6.78)
	unlisted := placed("Niemandes Mitglied", 51.22, 6.77)
	unplaced := organisation(t, s, "Ohne Ort", "")
	member(published.ID, verdi.ID)
	member(published.ID, linke.ID)
	member(published.ID, unplaced.ID)
	member(draft.ID, hidden.ID)
	member(draft.ID, verdi.ID)
	_ = unlisted

	everywhere, total, err := s.ListOrganisations(ctx, OrganisationQuery{Listed: true})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(everywhere) != 3 {
		t.Errorf("listed by a published collective: %d of %d, want 3 — the draft's own member and nobody's are not public", len(everywhere), total)
	}

	box := geo.Box{North: 51.3, South: 51.1, East: 6.9, West: 6.6}
	inDuesseldorf, total, err := s.ListOrganisations(ctx, OrganisationQuery{Listed: true, Bounds: &box})
	if err != nil {
		t.Fatal(err)
	}
	if len(inDuesseldorf) != 1 || inDuesseldorf[0].ID != verdi.ID || total != 3 {
		t.Errorf("in Düsseldorf: %v of %d, want ver.di of 3 — the count is of everywhere", names(inDuesseldorf), total)
	}

	of, err := s.PublishedCollectivesOf(ctx, []string{verdi.ID, hidden.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(of[verdi.ID]) != 1 || of[verdi.ID][0].ID != published.ID || len(of[hidden.ID]) != 0 {
		t.Errorf("collectives of ver.di %v, of the draft's member %v: want only the published one", of[verdi.ID], of[hidden.ID])
	}
}

func names(organisations []models.Organisation) []string {
	out := make([]string, 0, len(organisations))
	for _, organisation := range organisations {
		out = append(out, organisation.Name)
	}
	return out
}
