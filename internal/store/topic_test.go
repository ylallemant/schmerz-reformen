package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ylallemant/schmerz-reformen/internal/geo"
	"github.com/ylallemant/schmerz-reformen/internal/models"
)

// TestPublicationIsStampedOnce: a listing is ordered by it and followers are
// told because of it, so fixing a typo in something published last month must
// neither send it back to the top nor announce it a second time.
func TestPublicationIsStampedOnce(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	owner := collective(t, s, "Bündnis Düsseldorf")

	draft := &models.Topic{
		CollectiveID: owner.ID, Title: "100 Millionen weniger",
		Kind: models.TopicCut, Level: models.LevelMunicipal, Status: models.StatusDraft,
	}
	published, err := s.SaveTopic(ctx, draft)
	if err != nil {
		t.Fatalf("SaveTopic: %v", err)
	}
	if published || draft.PublishedAt != nil {
		t.Error("a draft was stamped as published")
	}

	draft.Status = models.StatusPublished
	published, err = s.SaveTopic(ctx, draft)
	if err != nil {
		t.Fatalf("SaveTopic: %v", err)
	}
	if !published || draft.PublishedAt == nil {
		t.Fatal("publishing did not stamp the topic")
	}
	first := *draft.PublishedAt

	draft.Title = "100 Millionen Euro weniger"
	published, err = s.SaveTopic(ctx, draft)
	if err != nil {
		t.Fatalf("SaveTopic: %v", err)
	}
	if published {
		t.Error("an edit to a published topic was reported as a publication")
	}
	if !draft.PublishedAt.Equal(first) {
		t.Error("an edit moved the publication time")
	}

	// Archived and published again is still the same publication.
	draft.Status = models.StatusArchived
	if _, err := s.SaveTopic(ctx, draft); err != nil {
		t.Fatalf("SaveTopic: %v", err)
	}
	draft.Status = models.StatusPublished
	if published, _ := s.SaveTopic(ctx, draft); published {
		t.Error("un-archiving was reported as a first publication")
	}
}

// TestTheMapNarrowsWhatIsDrawnNotWhatIsCounted: how many cuts there are and
// how widely they are spread is the argument, so a reader looking at one city
// is still told the size of the whole.
func TestTheMapNarrowsWhatIsDrawnNotWhatIsCounted(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	owner := collective(t, s, "Bündnis NRW")

	duesseldorf := topic(t, s, owner.ID, "Düsseldorf: 100 Millionen", 51.2277, 6.7735)
	topic(t, s, owner.ID, "Köln: Schwimmbad schließt", 50.9375, 6.9603)
	topic(t, s, owner.ID, "Berlin: Kita-Gebühren", 52.5200, 13.4050)

	// A federal reform with no pin: on the list, never on a map.
	unplaced := &models.Topic{
		CollectiveID: owner.ID, Title: "Bürgergeld", Kind: models.TopicReform,
		Level: models.LevelFederal, Status: models.StatusPublished,
	}
	if _, err := s.SaveTopic(ctx, unplaced); err != nil {
		t.Fatalf("SaveTopic: %v", err)
	}

	// A viewport over Düsseldorf alone.
	box := &geo.Box{South: 51.1, West: 6.6, North: 51.4, East: 6.95}
	got, total, err := s.ListTopics(ctx, TopicQuery{
		Statuses: []models.PublishStatus{models.StatusPublished}, Bounds: box,
	})
	if err != nil {
		t.Fatalf("ListTopics: %v", err)
	}
	if len(got) != 1 || got[0].ID != duesseldorf.ID {
		t.Errorf("drew %d topics, want only the one inside the viewport", len(got))
	}
	if total != 4 {
		t.Errorf("total = %d, want all 4 — a viewport must not shrink the count", total)
	}

	// No viewport: everything, including what has no pin.
	all, _, err := s.ListTopics(ctx, TopicQuery{Statuses: []models.PublishStatus{models.StatusPublished}})
	if err != nil {
		t.Fatalf("ListTopics: %v", err)
	}
	if len(all) != 4 {
		t.Errorf("listed %d topics, want 4", len(all))
	}

	// Filters do narrow the count: they are a question, not a window.
	cuts, total, err := s.ListTopics(ctx, TopicQuery{Kinds: []models.TopicKind{models.TopicReform}})
	if err != nil {
		t.Fatalf("ListTopics: %v", err)
	}
	if total != 1 || len(cuts) != 1 || cuts[0].ID != unplaced.ID {
		t.Errorf("kind filter returned %d of %d, want the one reform", len(cuts), total)
	}
}

// TestADraftIsNotInThePublicListing, whatever order it was written in.
func TestADraftIsNotInThePublicListing(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	owner := collective(t, s, "Bündnis Düsseldorf")

	topic(t, s, owner.ID, "Öffentlich", 51.2277, 6.7735)
	if _, err := s.SaveTopic(ctx, &models.Topic{
		CollectiveID: owner.ID, Title: "Entwurf", Status: models.StatusDraft,
	}); err != nil {
		t.Fatalf("SaveTopic: %v", err)
	}

	public, total, err := s.ListTopics(ctx, TopicQuery{
		Statuses: []models.PublishStatus{models.StatusPublished},
	})
	if err != nil {
		t.Fatalf("ListTopics: %v", err)
	}
	if total != 1 || len(public) != 1 || public[0].Title != "Öffentlich" {
		t.Errorf("public listing = %d topics, want the published one only", len(public))
	}

	// The console asks for every status and gets the draft too.
	if _, total, _ := s.ListTopics(ctx, TopicQuery{CollectiveID: owner.ID}); total != 2 {
		t.Errorf("console listing = %d, want both", total)
	}
}

// TestThePersonalFeedIsWhatWasFollowed: updates on followed topics and from
// followed collectives, each once, newest first.
func TestThePersonalFeedIsWhatWasFollowed(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	duesseldorf := collective(t, s, "Bündnis Düsseldorf")
	koeln := collective(t, s, "Bündnis Köln")
	berlin := collective(t, s, "Bündnis Berlin")

	budget := topic(t, s, duesseldorf.ID, "Haushalt", 51.2277, 6.7735)
	pool := topic(t, s, koeln.ID, "Schwimmbad", 50.9375, 6.9603)
	kita := topic(t, s, berlin.ID, "Kita", 52.52, 13.405)

	post := func(about models.Topic, title string, age time.Duration) {
		t.Helper()
		at := time.Now().Add(-age)
		update := &models.TopicUpdate{
			TopicID: about.ID, CollectiveID: about.CollectiveID, Title: title,
			Status: models.StatusPublished, PublishedAt: &at,
		}
		if _, err := s.SaveUpdate(ctx, update); err != nil {
			t.Fatalf("SaveUpdate(%q): %v", title, err)
		}
	}
	post(budget, "Rat vertagt", 3*time.Hour)
	post(pool, "Demo am Samstag", 2*time.Hour)
	post(kita, "Petition gestartet", time.Hour)

	reader := account(t, s, "Dominique")
	// Follows the Düsseldorf collective and the Cologne topic: once each,
	// through the two different doors.
	if err := s.Follow(ctx, reader.ID, models.FollowCollective, duesseldorf.ID); err != nil {
		t.Fatalf("Follow: %v", err)
	}
	if err := s.Follow(ctx, reader.ID, models.FollowTopic, pool.ID); err != nil {
		t.Fatalf("Follow: %v", err)
	}
	// And the Düsseldorf topic as well as its collective, which must not show
	// its update twice.
	if err := s.Follow(ctx, reader.ID, models.FollowTopic, budget.ID); err != nil {
		t.Fatalf("Follow: %v", err)
	}

	following, err := s.FollowingOf(ctx, reader.ID)
	if err != nil {
		t.Fatalf("FollowingOf: %v", err)
	}
	feed, total, err := s.ListUpdates(ctx, UpdateQuery{
		Statuses:  []models.PublishStatus{models.StatusPublished},
		Following: &following,
	})
	if err != nil {
		t.Fatalf("ListUpdates: %v", err)
	}
	if total != 2 || len(feed) != 2 {
		t.Fatalf("feed has %d of %d updates, want the two that were followed", len(feed), total)
	}
	if feed[0].Title != "Demo am Samstag" || feed[1].Title != "Rat vertagt" {
		t.Errorf("feed = %q then %q, want newest first", feed[0].Title, feed[1].Title)
	}

	// Following nothing is an empty feed, not everybody's.
	empty, total, err := s.ListUpdates(ctx, UpdateQuery{Following: &Following{}})
	if err != nil {
		t.Fatalf("ListUpdates: %v", err)
	}
	if total != 0 || len(empty) != 0 {
		t.Error("a reader who follows nothing was shown the whole feed")
	}

	// The public feed has all three.
	if _, total, _ := s.ListUpdates(ctx, UpdateQuery{}); total != 3 {
		t.Errorf("public feed = %d, want 3", total)
	}
}

// TestDeletingATopicKeepsItsActions: a demonstration is still happening on
// Saturday whether or not the page it pointed at exists.
func TestDeletingATopicKeepsItsActions(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	owner := collective(t, s, "Bündnis Düsseldorf")
	about := topic(t, s, owner.ID, "Haushalt", 51.2277, 6.7735)

	demo := action(t, s, owner.ID, "Demo", time.Now().Add(time.Hour))
	demo.TopicID = about.ID
	if _, err := s.SaveAction(ctx, &demo); err != nil {
		t.Fatalf("SaveAction: %v", err)
	}
	if _, err := s.SaveUpdate(ctx, &models.TopicUpdate{
		TopicID: about.ID, CollectiveID: owner.ID, Title: "Neu", Status: models.StatusPublished,
	}); err != nil {
		t.Fatalf("SaveUpdate: %v", err)
	}
	reader := account(t, s, "Dominique")
	if err := s.Follow(ctx, reader.ID, models.FollowTopic, about.ID); err != nil {
		t.Fatalf("Follow: %v", err)
	}

	if err := s.DeleteTopic(ctx, about.ID); err != nil {
		t.Fatalf("DeleteTopic: %v", err)
	}

	got, err := s.Action(ctx, demo.ID)
	if err != nil {
		t.Fatalf("the action went with its topic: %v", err)
	}
	if got.TopicID != "" {
		t.Errorf("the action still points at the deleted topic %q", got.TopicID)
	}
	if _, total, _ := s.ListUpdates(ctx, UpdateQuery{TopicID: about.ID}); total != 0 {
		t.Error("updates survived their topic")
	}
	if following, _ := s.FollowingOf(ctx, reader.ID); len(following.Topics) != 0 {
		t.Error("a follow survived the topic it pointed at")
	}
	if err := s.DeleteTopic(ctx, about.ID); !errors.Is(err, ErrTopicNotFound) {
		t.Errorf("err = %v, want ErrTopicNotFound the second time", err)
	}
}
