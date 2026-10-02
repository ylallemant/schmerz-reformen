package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ylallemant/schmerz-reformen/internal/models"
)

// TestACollectivesAddressIsItsOwn: the slug is in every link somebody printed
// on a flyer, so two collectives cannot answer at one — and the refusal has to
// be one an editor can be shown, not a constraint error.
func TestACollectivesAddressIsItsOwn(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	first := collective(t, s, "Bündnis Düsseldorf")
	if first.Slug != "buendnis-duesseldorf" {
		t.Errorf("slug = %q, want the umlauts folded rather than dropped", first.Slug)
	}

	// The same address however it is typed.
	second := &models.Collective{Name: "Another", Slug: "BÜNDNIS düsseldorf"}
	if err := s.CreateCollective(ctx, second); !errors.Is(err, ErrSlugTaken) {
		t.Errorf("err = %v, want ErrSlugTaken", err)
	}

	// Saving a collective under its own address is not a collision with itself.
	first.Summary = "Gegen die Kürzungen im Haushalt"
	if err := s.SaveCollective(ctx, &first); err != nil {
		t.Errorf("SaveCollective under its own slug: %v", err)
	}

	// Taking somebody else's is.
	other := collective(t, s, "Bündnis Köln")
	other.Slug = "buendnis-duesseldorf"
	if err := s.SaveCollective(ctx, &other); !errors.Is(err, ErrSlugTaken) {
		t.Errorf("err = %v, want ErrSlugTaken", err)
	}

	// And an address with nothing in it is no address.
	if err := s.CreateCollective(ctx, &models.Collective{Name: "!!!", Slug: "!!!"}); !errors.Is(err, ErrSlugTaken) {
		t.Errorf("err = %v, want an empty slug refused", err)
	}
}

// TestAnEditorSeesOnlyTheirOwnCollectives is the permission model in one
// query: the identity provider says which groups somebody is in, and that list
// is the whole of what they may open.
func TestAnEditorSeesOnlyTheirOwnCollectives(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	mine := collective(t, s, "Bündnis Düsseldorf")
	collective(t, s, "Bündnis Köln")

	got, total, err := s.ListCollectives(ctx, CollectiveQuery{AuthGroups: []string{mine.AuthGroup}})
	if err != nil {
		t.Fatalf("ListCollectives: %v", err)
	}
	if total != 1 || len(got) != 1 || got[0].ID != mine.ID {
		t.Errorf("got %d collectives, want only the one this group manages", len(got))
	}

	// An administrator passes no groups at all and sees both.
	if _, total, _ := s.ListCollectives(ctx, CollectiveQuery{}); total != 2 {
		t.Errorf("total = %d, want both", total)
	}

	// **In no group is not the same as unrestricted.** Reading an empty list
	// as "no filter" would hand everything to exactly the person with the
	// least claim to it.
	none, total, err := s.ListCollectives(ctx, CollectiveQuery{AuthGroups: []string{}})
	if err != nil {
		t.Fatalf("ListCollectives: %v", err)
	}
	if total != 0 || len(none) != 0 {
		t.Error("an editor in no group was shown collectives")
	}
}

// TestMembersKeepTheOrderTheCollectiveChose: who is named first in an alliance
// is a political decision, and not one to make alphabetically on its behalf.
func TestMembersKeepTheOrderTheCollectiveChose(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	owner := collective(t, s, "Bündnis Düsseldorf")

	for _, name := range []string{"ver.di", "Mieterverein", "Attac"} {
		member := &models.CollectiveMember{
			CollectiveID: owner.ID, Name: name, Kind: models.MemberUnion,
		}
		if err := s.SaveMember(ctx, member); err != nil {
			t.Fatalf("SaveMember(%q): %v", name, err)
		}
	}

	got, err := s.Collective(ctx, owner.ID)
	if err != nil {
		t.Fatalf("Collective: %v", err)
	}
	if len(got.Members) != 3 {
		t.Fatalf("%d members, want 3", len(got.Members))
	}
	for i, want := range []string{"ver.di", "Mieterverein", "Attac"} {
		if got.Members[i].Name != want {
			t.Errorf("member %d = %q, want %q — the order they were added", i, got.Members[i].Name, want)
		}
	}

	// Moved to the front by the collective.
	last := got.Members[2]
	last.Position = 0
	if err := s.SaveMember(ctx, &last); err != nil {
		t.Fatalf("SaveMember: %v", err)
	}
	got, _ = s.Collective(ctx, owner.ID)
	if got.Members[0].Name != "Attac" {
		t.Errorf("first member = %q, want the one moved to the front", got.Members[0].Name)
	}

	if err := s.DeleteMember(ctx, last.ID); err != nil {
		t.Fatalf("DeleteMember: %v", err)
	}
	if err := s.DeleteMember(ctx, last.ID); !errors.Is(err, ErrMemberNotFound) {
		t.Errorf("err = %v, want ErrMemberNotFound the second time", err)
	}
}

// TestDeletingACollectiveLeavesNothingBehind: a follow pointing at nothing is
// a row about a reader that serves no reader, and an image whose row is gone
// is a blob nobody will ever reclaim.
func TestDeletingACollectiveLeavesNothingBehind(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	doomed := collective(t, s, "Bündnis Düsseldorf")
	kept := collective(t, s, "Bündnis Köln")
	reader := account(t, s, "Dominique")

	about := topic(t, s, doomed.ID, "100 Millionen weniger", 51.2277, 6.7735)
	if _, err := s.SaveUpdate(ctx, &models.TopicUpdate{
		TopicID: about.ID, CollectiveID: doomed.ID, Title: "Vertagt",
		Status: models.StatusPublished,
	}); err != nil {
		t.Fatalf("SaveUpdate: %v", err)
	}
	demo := action(t, s, doomed.ID, "Demo", time.Now().Add(time.Hour))
	if err := s.SaveMember(ctx, &models.CollectiveMember{CollectiveID: doomed.ID, Name: "ver.di"}); err != nil {
		t.Fatalf("SaveMember: %v", err)
	}
	if err := s.CreateMedia(ctx, &models.Media{CollectiveID: doomed.ID, Key: "media/logo.png"}); err != nil {
		t.Fatalf("CreateMedia: %v", err)
	}

	elsewhere := topic(t, s, kept.ID, "Schwimmbad schließt", 50.9375, 6.9603)

	for _, follow := range []struct {
		target models.FollowTarget
		id     string
	}{
		{models.FollowCollective, doomed.ID},
		{models.FollowTopic, about.ID},
		{models.FollowTopic, elsewhere.ID},
	} {
		if err := s.Follow(ctx, reader.ID, follow.target, follow.id); err != nil {
			t.Fatalf("Follow: %v", err)
		}
	}
	if err := s.Participate(ctx, reader.ID, demo.ID); err != nil {
		t.Fatalf("Participate: %v", err)
	}

	keys, err := s.DeleteCollective(ctx, doomed.ID)
	if err != nil {
		t.Fatalf("DeleteCollective: %v", err)
	}
	if len(keys) != 1 || keys[0] != "media/logo.png" {
		t.Errorf("keys = %v, want the one image to remove from storage", keys)
	}

	for name, model := range map[string]any{
		"topics":  &models.Topic{},
		"updates": &models.TopicUpdate{},
		"actions": &models.Action{},
		"members": &models.CollectiveMember{},
		"media":   &models.Media{},
	} {
		var left int64
		s.DB().Model(model).Where("collective_id = ?", doomed.ID).Count(&left) //nolint:errcheck
		if left != 0 {
			t.Errorf("%d %s survived the collective", left, name)
		}
	}

	following, err := s.FollowingOf(ctx, reader.ID)
	if err != nil {
		t.Fatalf("FollowingOf: %v", err)
	}
	if len(following.Collectives) != 0 {
		t.Error("a follow of the deleted collective survived it")
	}
	if len(following.Topics) != 1 || following.Topics[0] != elsewhere.ID {
		t.Errorf("topics followed = %v, want only the one that still exists", following.Topics)
	}
	if left, _ := s.ParticipationsOf(ctx, reader.ID); len(left) != 0 {
		t.Error("an intent to attend a deleted action survived it")
	}

	if _, err := s.DeleteCollective(ctx, doomed.ID); !errors.Is(err, ErrCollectiveNotFound) {
		t.Errorf("err = %v, want ErrCollectiveNotFound the second time", err)
	}
	if _, err := s.Collective(ctx, kept.ID); err != nil {
		t.Errorf("the other collective went with it: %v", err)
	}
}
