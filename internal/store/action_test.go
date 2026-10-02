package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ylallemant/schmerz-reformen/internal/models"
)

// TestTheCalendarReadsForwardFromAGivenDay, in the order things happen.
func TestTheCalendarReadsForwardFromAGivenDay(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	owner := collective(t, s, "Bündnis Düsseldorf")

	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	action(t, s, owner.ID, "gestern", day.Add(-6*time.Hour))
	action(t, s, owner.ID, "abends", day.Add(18*time.Hour))
	action(t, s, owner.ID, "morgens", day.Add(9*time.Hour))
	action(t, s, owner.ID, "nächste Woche", day.Add(7*24*time.Hour))

	// A draft on the same day never reaches the calendar.
	if _, err := s.SaveAction(ctx, &models.Action{
		CollectiveID: owner.ID, Title: "Entwurf", StartsAt: day.Add(12 * time.Hour),
		Status: models.StatusDraft,
	}); err != nil {
		t.Fatalf("SaveAction: %v", err)
	}

	got, total, err := s.ListActions(ctx, ActionQuery{
		Statuses: []models.PublishStatus{models.StatusPublished},
		From:     day, To: day.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("ListActions: %v", err)
	}
	if total != 2 || len(got) != 2 {
		t.Fatalf("%d actions that day, want 2", total)
	}
	if got[0].Title != "morgens" || got[1].Title != "abends" {
		t.Errorf("order = %q then %q, want the order they happen", got[0].Title, got[1].Title)
	}

	// From is inclusive and To is not: midnight belongs to the day it starts.
	midnight := action(t, s, owner.ID, "Mitternacht", day.Add(24*time.Hour))
	got, _, _ = s.ListActions(ctx, ActionQuery{From: day, To: day.Add(24 * time.Hour),
		Statuses: []models.PublishStatus{models.StatusPublished}})
	for _, found := range got {
		if found.ID == midnight.ID {
			t.Error("an action at the next midnight was counted in the day before")
		}
	}
	got, _, _ = s.ListActions(ctx, ActionQuery{From: day.Add(24 * time.Hour),
		To: day.Add(48 * time.Hour), Statuses: []models.PublishStatus{models.StatusPublished}})
	if len(got) != 1 || got[0].ID != midnight.ID {
		t.Error("an action at midnight was not counted in the day it starts")
	}
}

// TestSayingYouAreComingTwiceIsSayingItOnce: a page left open in another tab
// offering the button again is not a mistake anybody made.
func TestSayingYouAreComingTwiceIsSayingItOnce(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	owner := collective(t, s, "Bündnis Düsseldorf")
	demo := action(t, s, owner.ID, "Demo", time.Now().Add(time.Hour))
	other := action(t, s, owner.ID, "Treffen", time.Now().Add(2*time.Hour))

	dominique := account(t, s, "Dominique")
	camille := account(t, s, "Camille")

	for range 2 {
		if err := s.Participate(ctx, dominique.ID, demo.ID); err != nil {
			t.Fatalf("Participate: %v", err)
		}
	}
	if err := s.Participate(ctx, camille.ID, demo.ID); err != nil {
		t.Fatalf("Participate: %v", err)
	}

	counts, err := s.ParticipantCounts(ctx, []string{demo.ID, other.ID})
	if err != nil {
		t.Fatalf("ParticipantCounts: %v", err)
	}
	if counts[demo.ID] != 2 {
		t.Errorf("count = %d, want 2 — one per account, however often it was pressed", counts[demo.ID])
	}
	if counts[other.ID] != 0 {
		t.Errorf("count = %d for an action nobody answered for", counts[other.ID])
	}

	// Withdrawing is the reader's, and withdrawing twice is not an error.
	for range 2 {
		if err := s.Withdraw(ctx, dominique.ID, demo.ID); err != nil {
			t.Fatalf("Withdraw: %v", err)
		}
	}
	accounts, err := s.ParticipantsOf(ctx, demo.ID)
	if err != nil {
		t.Fatalf("ParticipantsOf: %v", err)
	}
	if len(accounts) != 1 || accounts[0] != camille.ID {
		t.Errorf("participants = %v, want only the one who is still coming", accounts)
	}

	// The reader's own list is what feeds "my calendar" — and coming to
	// nothing is an empty calendar, not everybody's.
	coming, _ := s.ParticipationsOf(ctx, camille.ID)
	mine, total, err := s.ListActions(ctx, ActionQuery{IDs: coming})
	if err != nil {
		t.Fatalf("ListActions: %v", err)
	}
	if total != 1 || len(mine) != 1 || mine[0].ID != demo.ID {
		t.Errorf("my calendar has %d actions, want the one I am coming to", len(mine))
	}

	none, total, err := s.ListActions(ctx, ActionQuery{IDs: []string{}})
	if err != nil {
		t.Fatalf("ListActions: %v", err)
	}
	if total != 0 || len(none) != 0 {
		t.Error("a reader coming to nothing was shown every action")
	}
}

// TestFollowersAreToldOnce: somebody who follows both the alliance and the
// topic asked twice to be told and should be told once.
func TestFollowersAreToldOnce(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	owner := collective(t, s, "Bündnis Düsseldorf")
	about := topic(t, s, owner.ID, "Haushalt", 51.2277, 6.7735)
	unrelated := topic(t, s, owner.ID, "Schwimmbad", 51.2, 6.8)

	both := account(t, s, "Dominique")
	onlyTopic := account(t, s, "Camille")
	elsewhere := account(t, s, "Alex")

	for _, follow := range []struct {
		who    models.Account
		target models.FollowTarget
		id     string
	}{
		{both, models.FollowCollective, owner.ID},
		{both, models.FollowTopic, about.ID},
		{both, models.FollowTopic, about.ID}, // pressed twice
		{onlyTopic, models.FollowTopic, about.ID},
		{elsewhere, models.FollowTopic, unrelated.ID},
	} {
		if err := s.Follow(ctx, follow.who.ID, follow.target, follow.id); err != nil {
			t.Fatalf("Follow: %v", err)
		}
	}

	accounts, err := s.FollowersOf(ctx, owner.ID, about.ID)
	if err != nil {
		t.Fatalf("FollowersOf: %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("%d accounts to tell, want 2: %v", len(accounts), accounts)
	}
	for _, id := range accounts {
		if id == elsewhere.ID {
			t.Error("somebody following a different topic was told about this one")
		}
	}

	// With no topic, only the collective's own followers.
	accounts, err = s.FollowersOf(ctx, owner.ID, "")
	if err != nil {
		t.Fatalf("FollowersOf: %v", err)
	}
	if len(accounts) != 1 || accounts[0] != both.ID {
		t.Errorf("accounts = %v, want the collective's one follower", accounts)
	}

	if count, _ := s.CountFollowers(ctx, models.FollowTopic, about.ID); count != 2 {
		t.Errorf("followers = %d, want 2", count)
	}
	if follows, _ := s.Follows(ctx, both.ID, models.FollowTopic, about.ID); !follows {
		t.Error("Follows = false for something followed")
	}
	if err := s.Unfollow(ctx, both.ID, models.FollowTopic, about.ID); err != nil {
		t.Fatalf("Unfollow: %v", err)
	}
	if follows, _ := s.Follows(ctx, both.ID, models.FollowTopic, about.ID); follows {
		t.Error("Follows = true after unfollowing")
	}
}

// TestDeletingAnActionTakesItsIntents.
func TestDeletingAnActionTakesItsIntents(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	owner := collective(t, s, "Bündnis Düsseldorf")
	demo := action(t, s, owner.ID, "Demo", time.Now().Add(time.Hour))
	reader := account(t, s, "Dominique")

	if err := s.Participate(ctx, reader.ID, demo.ID); err != nil {
		t.Fatalf("Participate: %v", err)
	}
	if err := s.DeleteAction(ctx, demo.ID); err != nil {
		t.Fatalf("DeleteAction: %v", err)
	}
	if left, _ := s.ParticipationsOf(ctx, reader.ID); len(left) != 0 {
		t.Error("an intent survived the action it was for")
	}
	if err := s.DeleteAction(ctx, demo.ID); !errors.Is(err, ErrActionNotFound) {
		t.Errorf("err = %v, want ErrActionNotFound the second time", err)
	}
}
