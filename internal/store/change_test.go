package store

import (
	"context"
	"errors"
	"testing"

	"github.com/ylallemant/schmerz-reformen/internal/models"
)

const approvalsNeeded = 3

// propose records a change as an editor called author.
func propose(t *testing.T, s *Store, change models.OrganisationChange) models.OrganisationChange {
	t.Helper()
	if change.Author == "" {
		change.Author = "author"
	}
	if err := s.ProposeChange(context.Background(), &change); err != nil {
		t.Fatalf("ProposeChange(%s): %v", change.Kind, err)
	}
	return change
}

// vote casts one vote as voter and returns what it led to.
func vote(t *testing.T, s *Store, changeID, voter string, approve bool) Outcome {
	t.Helper()
	outcome, err := s.Vote(context.Background(), changeID, models.ChangeVote{
		Voter: voter, VoterName: voter, Approve: approve, Comment: "because",
	}, approvalsNeeded)
	if err != nil {
		t.Fatalf("Vote(%s, approve=%v): %v", voter, approve, err)
	}
	return outcome
}

// TestACreationWaitsForThreeOtherEditors: nothing exists until three editors
// who did not propose it have approved it, and the author is not one of them.
func TestACreationWaitsForThreeOtherEditors(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	change := propose(t, s, models.OrganisationChange{
		Kind:  models.ChangeCreate,
		After: models.OrganisationValues{Name: "ver.di Düsseldorf", Kind: models.MemberUnion},
	})
	if change.OrganisationID == "" {
		t.Fatal("a creation was proposed without the identifier it will create")
	}

	if _, err := s.Vote(ctx, change.ID, models.ChangeVote{Voter: "author", Approve: true}, approvalsNeeded); !errors.Is(err, ErrOwnChange) {
		t.Errorf("the author voted on their own change: err = %v", err)
	}

	for _, voter := range []string{"anna", "ben"} {
		if outcome := vote(t, s, change.ID, voter, true); outcome.Decided {
			t.Fatalf("decided after %s's vote, with fewer than %d approvals", voter, approvalsNeeded)
		}
		if _, err := s.Organisation(ctx, change.OrganisationID); !errors.Is(err, ErrOrganisationNotFound) {
			t.Fatalf("the organisation exists before it was approved: %v", err)
		}
	}

	if _, err := s.Vote(ctx, change.ID, models.ChangeVote{Voter: "anna", Approve: true}, approvalsNeeded); !errors.Is(err, ErrAlreadyVoted) {
		t.Errorf("one editor voted twice: err = %v", err)
	}

	outcome := vote(t, s, change.ID, "carla", true)
	if !outcome.Decided || outcome.Change.Status != models.ChangeApplied {
		t.Fatalf("after three approvals: decided=%v status=%s", outcome.Decided, outcome.Change.Status)
	}
	created, err := s.Organisation(ctx, change.OrganisationID)
	if err != nil {
		t.Fatalf("the approved organisation does not exist: %v", err)
	}
	if created.Name != "ver.di Düsseldorf" {
		t.Errorf("created %q", created.Name)
	}

	if _, err := s.Vote(ctx, change.ID, models.ChangeVote{Voter: "dana", Approve: true}, approvalsNeeded); !errors.Is(err, ErrChangeClosed) {
		t.Errorf("a decided change took another vote: err = %v", err)
	}
}

// TestThreeRejectionsCloseAChange, and the image uploaded with it is given
// back to be removed.
func TestThreeRejectionsCloseAChange(t *testing.T) {
	s := newStore(t)
	union := organisation(t, s, "ver.di", "")

	change := propose(t, s, models.OrganisationChange{
		Kind: models.ChangeUpdate, OrganisationID: union.ID,
		After: models.OrganisationValues{Name: union.Name, Kind: union.Kind, LogoID: "new-logo"},
	})

	vote(t, s, change.ID, "anna", true) // one approval does not save it
	for _, voter := range []string{"ben", "carla"} {
		vote(t, s, change.ID, voter, false)
	}
	outcome := vote(t, s, change.ID, "dana", false)
	if outcome.Change.Status != models.ChangeRejected {
		t.Fatalf("status = %s after three rejections", outcome.Change.Status)
	}
	if len(outcome.Unused) != 1 || outcome.Unused[0] != "new-logo" {
		t.Errorf("unused = %v, want the rejected upload", outcome.Unused)
	}

	kept, _ := s.Organisation(context.Background(), union.ID)
	if kept.LogoID != "" {
		t.Error("a rejected logo was applied")
	}
}

// TestAnUpdateAppliesOnlyWhatItChanged: two changes to different fields, each
// approved, both survive — whichever was approved first.
func TestAnUpdateAppliesOnlyWhatItChanged(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	union := organisation(t, s, "ver.di", "")

	values := union.OrganisationValues
	values.Name = "ver.di Bezirk Düsseldorf"
	rename := propose(t, s, models.OrganisationChange{
		Kind: models.ChangeUpdate, OrganisationID: union.ID, After: values,
	})
	if rename.Fields != models.FieldName {
		t.Errorf("fields = %q, want only the name — the form posted everything", rename.Fields)
	}

	values = union.OrganisationValues
	values.Website = "https://example.org/verdi"
	website := propose(t, s, models.OrganisationChange{
		Kind: models.ChangeUpdate, OrganisationID: union.ID, After: values,
	})

	// Another rename while one is waiting would be approved against a name
	// that is about to change.
	values = union.OrganisationValues
	values.Name = "Something else"
	err := s.ProposeChange(ctx, &models.OrganisationChange{
		Kind: models.ChangeUpdate, OrganisationID: union.ID, After: values, Author: "author",
	})
	if !errors.Is(err, ErrChangeConflict) {
		t.Errorf("a second pending rename: err = %v, want ErrChangeConflict", err)
	}

	err = s.ProposeChange(ctx, &models.OrganisationChange{
		Kind: models.ChangeUpdate, OrganisationID: union.ID, After: union.OrganisationValues, Author: "author",
	})
	if !errors.Is(err, ErrNothingChanged) {
		t.Errorf("a change to nothing: err = %v, want ErrNothingChanged", err)
	}

	for _, change := range []models.OrganisationChange{website, rename} {
		for _, voter := range []string{"anna", "ben", "carla"} {
			vote(t, s, change.ID, voter, true)
		}
	}

	got, _ := s.Organisation(ctx, union.ID)
	if got.Name != "ver.di Bezirk Düsseldorf" || got.Website != "https://example.org/verdi" {
		t.Errorf("got %q / %q, want both changes", got.Name, got.Website)
	}
}

// TestAParentChainNeverLoops: an organisation cannot be part of itself,
// directly or through another.
func TestAParentChainNeverLoops(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	federation := organisation(t, s, "ver.di", "")
	district := organisation(t, s, "ver.di NRW", federation.ID)
	local := organisation(t, s, "ver.di Düsseldorf", district.ID)

	for name, parent := range map[string]string{
		"itself":           federation.ID,
		"its child":        district.ID,
		"its grandchild":   local.ID,
		"a missing parent": "no-such-organisation",
	} {
		values := federation.OrganisationValues
		values.ParentID = parent
		err := s.ProposeChange(ctx, &models.OrganisationChange{
			Kind: models.ChangeUpdate, OrganisationID: federation.ID, After: values, Author: "author",
		})
		want := ErrParentLoop
		if parent == "no-such-organisation" {
			want = ErrParentNotFound
		}
		if !errors.Is(err, want) {
			t.Errorf("parent = %s: err = %v, want %v", name, err, want)
		}
	}
}

// TestAnApprovedChangeThatNoLongerFitsFails: checked again at the third vote,
// because what it depends on may have gone meanwhile.
func TestAnApprovedChangeThatNoLongerFitsFails(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	parent := organisation(t, s, "ver.di", "")

	change := propose(t, s, models.OrganisationChange{
		Kind:  models.ChangeCreate,
		After: models.OrganisationValues{Name: "ver.di Köln", Kind: models.MemberUnion, ParentID: parent.ID},
	})

	// Gone between the proposal and the decision.
	if err := s.DB().Delete(&models.Organisation{}, "id = ?", parent.ID).Error; err != nil {
		t.Fatal(err)
	}

	for _, voter := range []string{"anna", "ben"} {
		vote(t, s, change.ID, voter, true)
	}
	outcome := vote(t, s, change.ID, "carla", true)
	if outcome.Change.Status != models.ChangeFailed || outcome.Change.Reason == "" {
		t.Fatalf("status = %s, reason = %q; want failed, with the reason", outcome.Change.Status, outcome.Change.Reason)
	}
	if _, err := s.Organisation(ctx, change.OrganisationID); !errors.Is(err, ErrOrganisationNotFound) {
		t.Error("a change that failed was applied anyway")
	}

	stored, _ := s.Change(ctx, change.ID)
	if stored.Status != models.ChangeFailed || len(stored.Votes) != 3 {
		t.Errorf("stored as %s with %d votes; the votes must be kept", stored.Status, len(stored.Votes))
	}
}

// TestAnOrganisationInUseCannotBeDeleted, and one that is not is deleted with
// its logo, closing whatever else was waiting for it.
func TestAnOrganisationInUseCannotBeDeleted(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	owner := collective(t, s, "Bündnis Düsseldorf")
	member := organisation(t, s, "ver.di", "")
	if err := s.SaveMember(ctx, &models.CollectiveMember{CollectiveID: owner.ID, OrganisationID: member.ID}); err != nil {
		t.Fatal(err)
	}
	parent := organisation(t, s, "Attac", "")
	organisation(t, s, "Attac Düsseldorf", parent.ID)

	for _, used := range []models.Organisation{member, parent} {
		err := s.ProposeChange(ctx, &models.OrganisationChange{
			Kind: models.ChangeDelete, OrganisationID: used.ID, Author: "author",
		})
		if !errors.Is(err, ErrOrganisationInUse) {
			t.Errorf("deleting %q: err = %v, want ErrOrganisationInUse", used.Name, err)
		}
	}

	free := organisation(t, s, "Mieterverein", "")
	if err := s.DB().Model(&models.Organisation{}).Where("id = ?", free.ID).
		Update("logo_id", "old-logo").Error; err != nil {
		t.Fatal(err)
	}
	values := free.OrganisationValues
	values.Website = "https://example.org"
	waiting := propose(t, s, models.OrganisationChange{
		Kind: models.ChangeUpdate, OrganisationID: free.ID, After: values,
	})

	// A deletion conflicts with the change already waiting.
	if err := s.ProposeChange(ctx, &models.OrganisationChange{
		Kind: models.ChangeDelete, OrganisationID: free.ID, Author: "author",
	}); !errors.Is(err, ErrChangeConflict) {
		t.Fatalf("a deletion beside a pending change: err = %v, want ErrChangeConflict", err)
	}
	if _, err := s.WithdrawChange(ctx, waiting.ID, "author"); err != nil {
		t.Fatal(err)
	}

	removal := propose(t, s, models.OrganisationChange{Kind: models.ChangeDelete, OrganisationID: free.ID})
	var outcome Outcome
	for _, voter := range []string{"anna", "ben", "carla"} {
		outcome = vote(t, s, removal.ID, voter, true)
	}
	if outcome.Change.Status != models.ChangeApplied {
		t.Fatalf("status = %s", outcome.Change.Status)
	}
	if _, err := s.Organisation(ctx, free.ID); !errors.Is(err, ErrOrganisationNotFound) {
		t.Error("the organisation survived its approved deletion")
	}
	if len(outcome.Unused) != 1 || outcome.Unused[0] != "old-logo" {
		t.Errorf("unused = %v, want the deleted organisation's logo", outcome.Unused)
	}
}

// TestOnlyTheAuthorWithdraws a change, and only while it is pending.
func TestOnlyTheAuthorWithdraws(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	change := propose(t, s, models.OrganisationChange{
		Kind:  models.ChangeCreate,
		After: models.OrganisationValues{Name: "Initiative", Kind: models.MemberInitiative, LogoID: "upload"},
	})

	if _, err := s.WithdrawChange(ctx, change.ID, "somebody-else"); !errors.Is(err, ErrNotAuthor) {
		t.Errorf("somebody else withdrew it: err = %v", err)
	}
	outcome, err := s.WithdrawChange(ctx, change.ID, "author")
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Change.Status != models.ChangeWithdrawn || len(outcome.Unused) != 1 {
		t.Errorf("status = %s, unused = %v", outcome.Change.Status, outcome.Unused)
	}
	if _, err := s.WithdrawChange(ctx, change.ID, "author"); !errors.Is(err, ErrChangeClosed) {
		t.Errorf("withdrawn twice: err = %v", err)
	}
}

// TestAReplacedLogoIsGivenBack once the new one is applied.
func TestAReplacedLogoIsGivenBack(t *testing.T) {
	s := newStore(t)
	union := organisation(t, s, "ver.di", "")
	if err := s.DB().Model(&models.Organisation{}).Where("id = ?", union.ID).
		Update("logo_id", "old").Error; err != nil {
		t.Fatal(err)
	}
	union.LogoID = "old"

	values := union.OrganisationValues
	values.LogoID = "new"
	change := propose(t, s, models.OrganisationChange{Kind: models.ChangeUpdate, OrganisationID: union.ID, After: values})

	var outcome Outcome
	for _, voter := range []string{"anna", "ben", "carla"} {
		outcome = vote(t, s, change.ID, voter, true)
	}
	if len(outcome.Unused) != 1 || outcome.Unused[0] != "old" {
		t.Errorf("unused = %v, want the replaced logo", outcome.Unused)
	}
}

// TestAMemberIsListedOnce per collective.
func TestAMemberIsListedOnce(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	owner := collective(t, s, "Bündnis")
	union := organisation(t, s, "ver.di", "")

	first := &models.CollectiveMember{CollectiveID: owner.ID, OrganisationID: union.ID}
	if err := s.SaveMember(ctx, first); err != nil {
		t.Fatal(err)
	}
	err := s.SaveMember(ctx, &models.CollectiveMember{CollectiveID: owner.ID, OrganisationID: union.ID})
	if !errors.Is(err, ErrAlreadyMember) {
		t.Errorf("listed twice: err = %v, want ErrAlreadyMember", err)
	}
	err = s.SaveMember(ctx, &models.CollectiveMember{CollectiveID: owner.ID, OrganisationID: "nothing"})
	if !errors.Is(err, ErrOrganisationNotFound) {
		t.Errorf("an organisation that does not exist: err = %v", err)
	}
}
