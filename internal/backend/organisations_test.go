package backend

import (
	"context"
	"net/http"
	"testing"

	"github.com/ylallemant/schmerz-reformen/internal/models"
	"github.com/ylallemant/schmerz-reformen/internal/store"
)

// approveAs casts one approval and returns the change as it then stands.
func approveAs(t *testing.T, a *API, ctx context.Context, changeID string) ChangeItem {
	t.Helper()
	in := &VoteInput{ID: changeID}
	in.Body.Approve = true
	out, err := a.staffVoteOnChange(ctx, in)
	if err != nil {
		t.Fatalf("staffVoteOnChange: %v", err)
	}
	return out.Body
}

// approved proposes an organisation and has three other editors approve it,
// the way every organisation comes to exist.
func approved(t *testing.T, a *API, name, parentID string) OrganisationItem {
	t.Helper()

	in := &ProposeOrganisationInput{}
	in.Body.Name = name
	in.Body.Kind = string(models.MemberUnion)
	in.Body.ParentID = parentID
	proposed, err := a.staffProposeOrganisation(editor("author"), in)
	if err != nil {
		t.Fatalf("staffProposeOrganisation(%q): %v", name, err)
	}

	for _, voter := range []string{"anna", "ben", "carla"} {
		approveAs(t, a, editor(voter), proposed.Body.ID)
	}
	out, err := a.staffGetOrganisation(editor("author"), &OrganisationIDInput{ID: proposed.Body.OrganisationID})
	if err != nil {
		t.Fatalf("the approved organisation %q cannot be read: %v", name, err)
	}
	return out.Body
}

// TestAnOrganisationExistsOnlyOnceThreeOthersAgree, through the routes the
// console calls.
func TestAnOrganisationExistsOnlyOnceThreeOthersAgree(t *testing.T) {
	a := newAPI(t)

	in := &ProposeOrganisationInput{}
	in.Body.Name = "ver.di Düsseldorf"
	in.Body.Kind = string(models.MemberUnion)
	in.Body.Website = "https://example.org/verdi"
	proposed, err := a.staffProposeOrganisation(editor("author"), in)
	if err != nil {
		t.Fatalf("staffProposeOrganisation: %v", err)
	}
	change := proposed.Body
	if change.Status != string(models.ChangePending) || !change.Mine || change.CanVote || change.Needed != 3 {
		t.Errorf("as proposed: status=%s mine=%v can_vote=%v needed=%d", change.Status, change.Mine, change.CanVote, change.Needed)
	}

	listed, err := a.staffListOrganisations(editor("anna"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Body.Organisations) != 0 {
		t.Error("an organisation waiting for approval was offered in the list")
	}

	own := &VoteInput{ID: change.ID}
	own.Body.Approve = true
	if _, err := a.staffVoteOnChange(editor("author"), own); statusOf(err) != http.StatusForbidden {
		t.Errorf("the author approved their own change: status %d", statusOf(err))
	}

	reject := &VoteInput{ID: change.ID}
	if _, err := a.staffVoteOnChange(editor("dana"), reject); statusOf(err) != http.StatusUnprocessableEntity {
		t.Errorf("a rejection without a reason: status %d, want 422", statusOf(err))
	}

	pending, err := a.staffListChanges(editor("anna"), &ChangesInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pending.Body.Changes) != 1 || !pending.Body.Changes[0].CanVote {
		t.Fatalf("the queue as another editor sees it: %+v", pending.Body.Changes)
	}

	approveAs(t, a, editor("anna"), change.ID)
	again := &VoteInput{ID: change.ID}
	again.Body.Approve = true
	if _, err := a.staffVoteOnChange(editor("anna"), again); statusOf(err) != http.StatusConflict {
		t.Errorf("one editor voted twice: status %d", statusOf(err))
	}
	approveAs(t, a, editor("ben"), change.ID)
	decided := approveAs(t, a, editor("carla"), change.ID)
	if decided.Status != string(models.ChangeApplied) || decided.Approvals != 3 {
		t.Fatalf("after three approvals: status=%s approvals=%d", decided.Status, decided.Approvals)
	}

	got, err := a.staffGetOrganisation(editor("anna"), &OrganisationIDInput{ID: change.OrganisationID})
	if err != nil {
		t.Fatalf("staffGetOrganisation: %v", err)
	}
	if got.Body.Name != "ver.di Düsseldorf" || got.Body.Website != "https://example.org/verdi" {
		t.Errorf("created %+v", got.Body.OrganisationValuesItem)
	}

	// The log says who proposed it, who let it through and what it did.
	entries, _, err := a.store.ListAudit(context.Background(), store.AuditQuery{})
	if err != nil {
		t.Fatal(err)
	}
	verbs := map[models.AuditAction]int{}
	for _, entry := range entries {
		verbs[entry.Action]++
	}
	if verbs[models.AuditPropose] != 1 || verbs[models.AuditApprove] != 3 || verbs[models.AuditCreate] != 1 {
		t.Errorf("audit verbs = %v", verbs)
	}
}

// TestAMemberLinksAnExistingOrganisation, and a collective's page shows the
// organisation as it is — including after an approved change to it.
func TestAMemberLinksAnExistingOrganisation(t *testing.T) {
	a := newAPI(t)
	owner := founded(t, a, "Bündnis Düsseldorf", "duesseldorf")
	union := approved(t, a, "ver.di", "")
	ctx := editor("maria", "duesseldorf")

	add := &CreateMemberInput{ID: owner.ID}
	add.Body.OrganisationID = "no-such-organisation"
	if _, err := a.staffCreateMember(ctx, add); statusOf(err) != http.StatusUnprocessableEntity {
		t.Errorf("adding an organisation that does not exist: status %d", statusOf(err))
	}

	add.Body.OrganisationID = union.ID
	member, err := a.staffCreateMember(ctx, add)
	if err != nil {
		t.Fatalf("staffCreateMember: %v", err)
	}
	if member.Body.Name != "ver.di" || member.Body.OrganisationID != union.ID {
		t.Errorf("member = %+v", member.Body)
	}
	if _, err := a.staffCreateMember(ctx, add); statusOf(err) != http.StatusConflict {
		t.Errorf("listing it twice: status %d", statusOf(err))
	}

	// Somebody else's editor cannot add to this collective.
	if _, err := a.staffCreateMember(editor("kai", "koeln"), add); statusOf(err) != http.StatusNotFound {
		t.Errorf("an editor of another collective added a member: status %d", statusOf(err))
	}

	rename := &ProposeOrganisationUpdateInput{ID: union.ID}
	rename.Body.Name = "ver.di Bezirk Düsseldorf"
	rename.Body.Kind = union.Kind
	proposed, err := a.staffProposeOrganisationUpdate(editor("author"), rename)
	if err != nil {
		t.Fatalf("staffProposeOrganisationUpdate: %v", err)
	}
	if len(proposed.Body.Fields) != 1 || proposed.Body.Fields[0] != models.FieldName {
		t.Errorf("fields = %v, want the name only", proposed.Body.Fields)
	}

	page, err := a.getCollective(anonymous(), &CollectiveSlugInput{Slug: owner.Slug})
	if err != nil {
		t.Fatal(err)
	}
	if page.Body.Members[0].Name != "ver.di" {
		t.Errorf("a pending rename is showing: %q", page.Body.Members[0].Name)
	}

	for _, voter := range []string{"anna", "ben", "carla"} {
		approveAs(t, a, editor(voter), proposed.Body.ID)
	}
	page, _ = a.getCollective(anonymous(), &CollectiveSlugInput{Slug: owner.Slug})
	if page.Body.Members[0].Name != "ver.di Bezirk Düsseldorf" {
		t.Errorf("the approved rename is not showing: %q", page.Body.Members[0].Name)
	}

	// Removing the member keeps the organisation.
	if _, err := a.staffDeleteMember(ctx, &MemberIDInput{ID: member.Body.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.staffGetOrganisation(ctx, &OrganisationIDInput{ID: union.ID}); err != nil {
		t.Errorf("the organisation went with the membership: %v", err)
	}
}

// TestAnOrganisationPageHidesOtherCollectivesDrafts: being listed in a draft
// collective is that collective's business until it is published.
func TestAnOrganisationPageHidesOtherCollectivesDrafts(t *testing.T) {
	a := newAPI(t)
	union := approved(t, a, "ver.di", "")

	public := founded(t, a, "Bündnis Düsseldorf", "duesseldorf")
	draftIn := &CreateCollectiveInput{}
	draftIn.Body.Name = "Bündnis in Vorbereitung"
	draftIn.Body.AuthGroup = "secret"
	draftIn.Body.Status = string(models.StatusDraft)
	draft, err := a.staffCreateCollective(admin(), draftIn)
	if err != nil {
		t.Fatal(err)
	}

	for _, collectiveID := range []string{public.ID, draft.Body.ID} {
		add := &CreateMemberInput{ID: collectiveID}
		add.Body.OrganisationID = union.ID
		if _, err := a.staffCreateMember(admin(), add); err != nil {
			t.Fatal(err)
		}
	}

	for name, tc := range map[string]struct {
		ctx  context.Context
		want int
	}{
		"an editor of neither":   {editor("kai", "koeln"), 1},
		"an editor of the draft": {editor("sam", "secret"), 2},
		"an administrator":       {admin(), 2},
	} {
		got, err := a.staffGetOrganisation(tc.ctx, &OrganisationIDInput{ID: union.ID})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Body.Collectives) != tc.want {
			t.Errorf("%s sees %d collectives, want %d", name, len(got.Body.Collectives), tc.want)
		}
	}
}

// TestAProposedLogoIsKeptAsideUntilApproved, and removed when withdrawn.
func TestAProposedLogoIsKeptAsideUntilApproved(t *testing.T) {
	a := newAPI(t)
	union := approved(t, a, "ver.di", "")
	png := []byte("\x89PNG\r\n\x1a\nnot really, but enough")

	proposed, err := a.staffProposeOrganisationLogo(editor("author"), &LogoInput{
		ID: union.ID, ContentType: "image/png", RawBody: png,
	})
	if err != nil {
		t.Fatalf("staffProposeOrganisationLogo: %v", err)
	}
	upload := proposed.Body.After.LogoID
	if upload == "" {
		t.Fatal("the proposed logo was not stored")
	}
	got, _ := a.staffGetOrganisation(editor("anna"), &OrganisationIDInput{ID: union.ID})
	if got.Body.LogoID != "" || len(got.Body.Pending) != 1 {
		t.Errorf("logo %q with %d pending; want none applied and one waiting", got.Body.LogoID, len(got.Body.Pending))
	}

	if _, err := a.staffWithdrawChange(editor("anna"), &ChangeIDInput{ID: proposed.Body.ID}); statusOf(err) != http.StatusForbidden {
		t.Errorf("somebody else withdrew it: status %d", statusOf(err))
	}
	if _, err := a.staffWithdrawChange(editor("author"), &ChangeIDInput{ID: proposed.Body.ID}); err != nil {
		t.Fatalf("staffWithdrawChange: %v", err)
	}
	if _, err := a.getMedia(anonymous(), &MediaIDInput{ID: upload}); statusOf(err) != http.StatusNotFound {
		t.Errorf("the withdrawn upload is still served: status %d", statusOf(err))
	}

	again, err := a.staffProposeOrganisationLogo(editor("author"), &LogoInput{
		ID: union.ID, ContentType: "image/png", RawBody: png,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, voter := range []string{"anna", "ben", "carla"} {
		approveAs(t, a, editor(voter), again.Body.ID)
	}
	got, _ = a.staffGetOrganisation(editor("anna"), &OrganisationIDInput{ID: union.ID})
	if got.Body.LogoID != again.Body.After.LogoID {
		t.Errorf("logo = %q, want the approved upload", got.Body.LogoID)
	}
	if _, err := a.getMedia(anonymous(), &MediaIDInput{ID: got.Body.LogoID}); err != nil {
		t.Errorf("the approved logo is not served: %v", err)
	}
}
