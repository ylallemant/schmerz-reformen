package backend

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/ylallemant/schmerz-reformen/internal/models"
	"github.com/ylallemant/schmerz-reformen/internal/store"
)

// statusOf reads the HTTP status a handler's error carries.
func statusOf(err error) int {
	var withStatus huma.StatusError
	if errors.As(err, &withStatus) {
		return withStatus.GetStatus()
	}
	return 0
}

// TestOnlyAnAdministratorFoundsACollective: creating one is deciding which
// group gets to publish on this site at all.
func TestOnlyAnAdministratorFoundsACollective(t *testing.T) {
	a := newAPI(t)

	in := &CreateCollectiveInput{}
	in.Body.Name = "Bündnis Düsseldorf"

	if _, err := a.staffCreateCollective(editor("Dominique", adminsOf("duesseldorf")), in); statusOf(err) != http.StatusForbidden {
		t.Errorf("an editor creating a collective: %v, want 403", err)
	}

	out, err := a.staffCreateCollective(admin(), in)
	if err != nil {
		t.Fatalf("an administrator creating a collective: %v", err)
	}
	if out.Body.Slug != "buendnis-duesseldorf" {
		t.Errorf("slug = %q, want it derived from the name", out.Body.Slug)
	}
	if out.Body.Status != string(models.StatusDraft) {
		t.Errorf("status = %q, want a new collective to start as a draft", out.Body.Status)
	}

	// The same address twice is a conflict an administrator can be shown.
	if _, err := a.staffCreateCollective(admin(), in); statusOf(err) != http.StatusConflict {
		t.Errorf("a second collective at the same address: %v, want 409", err)
	}
}

// TestAnEditorManagesTheirOwnCollectiveAndNoOther is the permission model.
//
// Somebody else's collective answers 404 rather than 403: an editor of one
// alliance has no business learning that another exists in draft.
func TestAnEditorManagesTheirOwnCollectiveAndNoOther(t *testing.T) {
	a := newAPI(t)
	mine := founded(t, a, "Bündnis Düsseldorf", "duesseldorf")
	theirs := founded(t, a, "Bündnis Köln", "koeln")
	me := editor("Dominique", adminsOf("duesseldorf"))

	listing, err := a.staffListCollectives(me, nil)
	if err != nil {
		t.Fatalf("staffListCollectives: %v", err)
	}
	if len(listing.Body.Collectives) != 1 || listing.Body.Collectives[0].ID != mine.ID {
		t.Errorf("an editor sees %d collectives, want only their own", len(listing.Body.Collectives))
	}

	// Somebody in no group at all sees nothing — not everything.
	nobody, err := a.staffListCollectives(editor("Nobody"), nil)
	if err != nil {
		t.Fatalf("staffListCollectives: %v", err)
	}
	if len(nobody.Body.Collectives) != 0 {
		t.Errorf("somebody in no group sees %d collectives", len(nobody.Body.Collectives))
	}

	if _, err := a.staffGetCollective(me, &CollectiveIDInput{ID: theirs.ID}); statusOf(err) != http.StatusNotFound {
		t.Errorf("reading another collective: %v, want 404", err)
	}

	topicIn := &CreateTopicInput{ID: theirs.ID}
	topicIn.Body.Title = "Nicht meins"
	topicIn.Body.Kind, topicIn.Body.Level = "cut", "municipal"
	if _, err := a.staffCreateTopic(me, topicIn); statusOf(err) != http.StatusNotFound {
		t.Errorf("publishing under another collective: %v, want 404", err)
	}

	// And everything hanging off another collective is as unreachable as the
	// collective itself.
	theirEditor := editor("Camille", adminsOf("koeln"))
	theirTopic := raised(t, a, theirEditor, theirs.ID, "Schwimmbad schließt")
	theirAction := announced(t, a, theirEditor, theirs.ID, "Demo", time.Now().Add(48*time.Hour))

	save := &SaveTopicInput{ID: theirTopic.ID}
	save.Body.Title, save.Body.Kind, save.Body.Level = "Übernommen", "cut", "municipal"
	if _, err := a.staffSaveTopic(me, save); statusOf(err) != http.StatusNotFound {
		t.Errorf("editing another collective's topic: %v, want 404", err)
	}
	if _, err := a.staffDeleteTopic(me, &TopicIDInput{ID: theirTopic.ID}); statusOf(err) != http.StatusNotFound {
		t.Errorf("deleting another collective's topic: %v, want 404", err)
	}
	if _, err := a.staffDeleteAction(me, &ActionIDInput{ID: theirAction.ID}); statusOf(err) != http.StatusNotFound {
		t.Errorf("deleting another collective's action: %v, want 404", err)
	}
	updateIn := &CreateUpdateInput{ID: theirTopic.ID}
	updateIn.Body.Title, updateIn.Body.Body = "Falschmeldung", "…"
	if _, err := a.staffCreateUpdate(me, updateIn); statusOf(err) != http.StatusNotFound {
		t.Errorf("posting news on another collective's topic: %v, want 404", err)
	}

	// Their own content is untouched by all of that.
	if got, err := a.store.Topic(context.Background(), theirTopic.ID); err != nil || got.Title != "Schwimmbad schließt" {
		t.Errorf("the other collective's topic was changed: %+v, %v", got, err)
	}
}

// TestAnEditorCannotMoveACollectiveOrReassignIt.
//
// The address is in every link already printed, and the groups were named
// when the collective was created. An editor's form posts the address, so it
// is ignored rather than refused — saving a description must not be an error.
func TestAnEditorCannotMoveACollective(t *testing.T) {
	a := newAPI(t)
	mine := founded(t, a, "Bündnis Düsseldorf", "duesseldorf")

	in := &SaveCollectiveInput{ID: mine.ID}
	in.Body.Name = "Bündnis Düsseldorf gegen Kürzungen"
	in.Body.Summary = "Gewerkschaften, Mieterverein und Parteien gemeinsam."
	in.Body.Slug = "ganz-woanders"

	out, err := a.staffSaveCollective(editor("Dominique", adminsOf("duesseldorf")), in)
	if err != nil {
		t.Fatalf("staffSaveCollective: %v", err)
	}
	if out.Body.Name != in.Body.Name || out.Body.Summary != in.Body.Summary {
		t.Errorf("the profile was not saved: %+v", out.Body)
	}
	if out.Body.Slug != "duesseldorf" {
		t.Errorf("slug = %q: an editor moved the collective", out.Body.Slug)
	}

	// An administrator can move it — and its groups stay what they were, so
	// nobody in them loses their role.
	in.Body.Slug = "duesseldorf-neu"
	out, err = a.staffSaveCollective(admin(), in)
	if err != nil {
		t.Fatalf("staffSaveCollective as admin: %v", err)
	}
	if out.Body.Slug != "duesseldorf-neu" || out.Body.AdminGroup != adminsOf("duesseldorf") {
		t.Errorf("slug %q, admins %q: the groups must not follow the slug", out.Body.Slug, out.Body.AdminGroup)
	}
	if _, err := a.staffGetCollective(editor("Dominique", adminsOf("duesseldorf")),
		&CollectiveIDInput{ID: mine.ID}); err != nil {
		t.Errorf("the collective's administrator lost it when it moved: %v", err)
	}
}

// TestAnAuthorPublishesAndDoesNotAdminister: an author writes the topics,
// news and actions; the profile, the member organisations and who writes are
// the collective's administrators'.
func TestAnAuthorPublishesAndDoesNotAdminister(t *testing.T) {
	a := newAPI(t)
	mine := founded(t, a, "Bündnis Düsseldorf", "duesseldorf")
	author := editor("Kai", authorsOf("duesseldorf"))

	raised(t, a, author, mine.ID, "Haushalt 2027")

	in := &SaveCollectiveInput{ID: mine.ID}
	in.Body.Name = "Umbenannt"
	if _, err := a.staffSaveCollective(author, in); statusOf(err) != http.StatusForbidden {
		t.Errorf("an author saving the profile: status %d, want 403", statusOf(err))
	}
	add := &CreateMemberInput{ID: mine.ID}
	add.Body.OrganisationID = "anything"
	if _, err := a.staffCreateMember(author, add); statusOf(err) != http.StatusForbidden {
		t.Errorf("an author adding a member: status %d, want 403", statusOf(err))
	}
	got, err := a.staffGetCollective(author, &CollectiveIDInput{ID: mine.ID})
	if err != nil || !got.Body.Authors || got.Body.Administers {
		t.Errorf("an author reading it: %v, authors %v administers %v", err, got.Body.Authors, got.Body.Administers)
	}

	// Somebody with no role on it: not even told it exists.
	if _, err := a.staffGetCollective(editor("Nobody", authorsOf("koeln")), &CollectiveIDInput{ID: mine.ID}); statusOf(err) != http.StatusNotFound {
		t.Errorf("no role: status %d, want 404", statusOf(err))
	}
}

// TestAUserWritesNothing: being in the users' group lets somebody into the
// console so a collective or an organisation can add them — and does not, by
// itself, let them write a word.
func TestAUserWritesNothing(t *testing.T) {
	a := newAPI(t)
	mine := founded(t, a, "Bündnis Düsseldorf", "duesseldorf")
	user := editor("Uma", models.UsersGroupName(models.DefaultAppName))

	in := &CreateTopicInput{ID: mine.ID}
	in.Body.Title, in.Body.Kind, in.Body.Level = "Haushalt 2027", "cut", "municipal"
	if _, err := a.staffCreateTopic(user, in); err == nil {
		t.Error("a user with no role on the collective wrote a topic for it")
	}
	save := &SaveCollectiveInput{ID: mine.ID}
	save.Body.Name = "Umbenannt"
	if _, err := a.staffSaveCollective(user, save); err == nil {
		t.Error("a user with no role on the collective changed it")
	}
}

// TestADraftAnswersAsIfItDidNotExist: whether something is being prepared is
// not a reader's to learn from a status code.
func TestADraftAnswersAsIfItDidNotExist(t *testing.T) {
	a := newAPI(t)
	owner := founded(t, a, "Bündnis Düsseldorf", "duesseldorf")
	me := editor("Dominique", adminsOf("duesseldorf"))

	draftIn := &CreateTopicInput{ID: owner.ID}
	draftIn.Body.Title, draftIn.Body.Kind, draftIn.Body.Level = "Noch geheim", "cut", "municipal"
	draft, err := a.staffCreateTopic(me, draftIn)
	if err != nil {
		t.Fatalf("staffCreateTopic: %v", err)
	}

	if _, err := a.getTopic(anonymous(), &TopicIDInput{ID: draft.Body.ID}); statusOf(err) != http.StatusNotFound {
		t.Errorf("reading a draft topic: %v, want 404", err)
	}
	listing, err := a.listTopics(anonymous(), &TopicListInput{})
	if err != nil {
		t.Fatalf("listTopics: %v", err)
	}
	if len(listing.Body.Topics) != 0 {
		t.Errorf("the public listing shows %d topics, want none while it is a draft", len(listing.Body.Topics))
	}

	// A draft cannot be followed either: that would be a way to learn it
	// exists, and to be told the moment it is published.
	someone := reader(t, a, "Camille")
	if _, err := a.follow(as(someone), &FollowInput{Target: "topic", ID: draft.Body.ID}); statusOf(err) != http.StatusNotFound {
		t.Errorf("following a draft: %v, want 404", err)
	}

	// Its editors see it.
	if _, err := a.staffGetTopic(me, &TopicIDInput{ID: draft.Body.ID}); err != nil {
		t.Errorf("its own editor cannot read the draft: %v", err)
	}
}

// TestContentIsPublicOnlyWhileItsCollectiveIs: an alliance taken back to draft
// must not leave its pages up with no author.
func TestContentIsPublicOnlyWhileItsCollectiveIs(t *testing.T) {
	a := newAPI(t)
	owner := founded(t, a, "Bündnis Düsseldorf", "duesseldorf")
	me := editor("Dominique", adminsOf("duesseldorf"))
	about := raised(t, a, me, owner.ID, "100 Millionen weniger")
	demo := announced(t, a, me, owner.ID, "Demo", time.Now().Add(48*time.Hour))

	news := &CreateUpdateInput{ID: about.ID}
	news.Body.Title, news.Body.Body, news.Body.Status = "Rat vertagt", "Die Abstimmung ist verschoben.", "published"
	posted, err := a.staffCreateUpdate(me, news)
	if err != nil {
		t.Fatalf("staffCreateUpdate: %v", err)
	}

	public := func() (topics, updates, actions int) {
		t.Helper()
		topicList, err := a.listTopics(anonymous(), &TopicListInput{})
		if err != nil {
			t.Fatalf("listTopics: %v", err)
		}
		updateList, err := a.listUpdates(anonymous(), &UpdateListInput{})
		if err != nil {
			t.Fatalf("listUpdates: %v", err)
		}
		actionList, err := a.listActions(anonymous(), &ActionListInput{})
		if err != nil {
			t.Fatalf("listActions: %v", err)
		}
		return len(topicList.Body.Topics), len(updateList.Body.Updates), len(actionList.Body.Actions)
	}

	if topics, updates, actions := public(); topics != 1 || updates != 1 || actions != 1 {
		t.Fatalf("published: %d topics, %d updates, %d actions; want one of each", topics, updates, actions)
	}

	// Taken back to draft by an administrator.
	back := &SaveCollectiveInput{ID: owner.ID}
	back.Body.Name, back.Body.Status = owner.Name, string(models.StatusDraft)
	if _, err := a.staffSaveCollective(admin(), back); err != nil {
		t.Fatalf("staffSaveCollective: %v", err)
	}
	// The handler is called directly here, so the middleware that would have
	// dropped the cache after this write did not run.
	a.cache.Clear()

	if topics, updates, actions := public(); topics != 0 || updates != 0 || actions != 0 {
		t.Errorf("collective in draft: %d topics, %d updates, %d actions still public", topics, updates, actions)
	}
	for name, err := range map[string]error{
		"topic":  second(a.getTopic(anonymous(), &TopicIDInput{ID: about.ID})),
		"update": second(a.getUpdate(anonymous(), &UpdateIDInput{ID: posted.Body.ID})),
		"action": second(a.getAction(anonymous(), &ActionIDInput{ID: demo.ID})),
	} {
		if statusOf(err) != http.StatusNotFound {
			t.Errorf("the %s of a draft collective still answers: %v", name, err)
		}
	}
}

// second returns the error of a two-valued call, for collecting several.
func second[T any](_ T, err error) error { return err }

// TestTheCachedListingDoesNotLendOneReadersAnswersToAnother is the failure the
// cache was written to be incapable of, asserted rather than asserted about.
//
// The calendar is cached and shared by everybody, which is only safe because
// nothing in it is per-reader. "Am I coming" is per-reader, so it is computed
// on top of the cached rows rather than stored with them. If that ever moved
// inside the cache this is what notices — and what it would be noticing is one
// person's political calendar shown to a stranger.
func TestTheCachedListingDoesNotLendOneReadersAnswersToAnother(t *testing.T) {
	a := newAPI(t)
	owner := founded(t, a, "Bündnis Düsseldorf", "duesseldorf")
	demo := announced(t, a, editor("Dominique", adminsOf("duesseldorf")), owner.ID, "Demo", time.Now().Add(48*time.Hour))

	coming := reader(t, a, "Camille")
	other := reader(t, a, "Alex")

	if _, err := a.participate(as(coming), &ActionIDInput{ID: demo.ID}); err != nil {
		t.Fatalf("participate: %v", err)
	}

	read := func(ctx context.Context) ActionItem {
		t.Helper()
		out, err := a.listActions(ctx, &ActionListInput{})
		if err != nil {
			t.Fatalf("listActions: %v", err)
		}
		if len(out.Body.Actions) != 1 {
			t.Fatalf("%d actions, want 1", len(out.Body.Actions))
		}
		return out.Body.Actions[0]
	}

	// The reader who is coming first, which is what warms the cache.
	if got := read(as(coming)); !got.Participating || got.Participants != 1 {
		t.Fatalf("the reader who is coming sees %+v", got)
	}
	if got := read(as(other)); got.Participating {
		t.Error("another reader was shown somebody else's intent as their own")
	}
	if got := read(anonymous()); got.Participating {
		t.Error("an anonymous reader was shown somebody's intent")
	}
	// The count is the same for everybody, and exact.
	if got := read(as(other)); got.Participants != 1 {
		t.Errorf("participants = %d, want 1", got.Participants)
	}

	// The same for following, on a page that is read one at a time.
	follower := reader(t, a, "Sam")
	if _, err := a.follow(as(follower), &FollowInput{Target: "collective", ID: owner.ID}); err != nil {
		t.Fatalf("follow: %v", err)
	}
	mine, err := a.getCollective(as(follower), &CollectiveSlugInput{Slug: owner.Slug})
	if err != nil {
		t.Fatalf("getCollective: %v", err)
	}
	theirs, err := a.getCollective(as(other), &CollectiveSlugInput{Slug: owner.Slug})
	if err != nil {
		t.Fatalf("getCollective: %v", err)
	}
	if !mine.Body.Following || theirs.Body.Following {
		t.Errorf("following = %v for the follower and %v for somebody else", mine.Body.Following, theirs.Body.Following)
	}
	if mine.Body.Followers != 1 || theirs.Body.Followers != 1 {
		t.Errorf("followers = %d and %d, want the same count for both", mine.Body.Followers, theirs.Body.Followers)
	}
	// And the reader is never told which groups run a collective.
	if mine.Body.AdminGroup != "" || mine.Body.AuthorGroup != "" {
		t.Errorf("a reader was told the groups: %q / %q", mine.Body.AdminGroup, mine.Body.AuthorGroup)
	}
}

// TestNewsIsToldOnceToWhoeverFollows.
func TestNewsIsToldOnceToWhoeverFollows(t *testing.T) {
	a := newAPI(t)
	owner := founded(t, a, "Bündnis Düsseldorf", "duesseldorf")
	me := editor("Dominique", adminsOf("duesseldorf"))
	about := raised(t, a, me, owner.ID, "100 Millionen weniger")

	both := reader(t, a, "Camille")      // follows the collective and the topic
	topicOnly := reader(t, a, "Alex")    // follows the topic
	stranger := reader(t, a, "Stranger") // follows nothing

	for _, follow := range []struct {
		who    *caller
		target string
		id     string
	}{
		{both, "collective", owner.ID}, {both, "topic", about.ID}, {topicOnly, "topic", about.ID},
	} {
		if _, err := a.follow(as(follow.who), &FollowInput{Target: follow.target, ID: follow.id}); err != nil {
			t.Fatalf("follow: %v", err)
		}
	}

	// A draft says nothing to anybody.
	news := &CreateUpdateInput{ID: about.ID}
	news.Body.Title, news.Body.Body = "Rat vertagt", "Die Abstimmung ist auf November verschoben."
	draft, err := a.staffCreateUpdate(me, news)
	if err != nil {
		t.Fatalf("staffCreateUpdate: %v", err)
	}
	if told := notificationsOf(t, a, both); len(told) != 0 {
		t.Fatalf("a draft update told %d people", len(told))
	}

	// Publishing it tells each follower once, however many ways they follow.
	publish := &SaveUpdateInput{ID: draft.Body.ID}
	publish.Body = news.Body
	publish.Body.Status = "published"
	if _, err := a.staffSaveUpdate(me, publish); err != nil {
		t.Fatalf("staffSaveUpdate: %v", err)
	}

	for name, who := range map[string]*caller{"both": both, "topic only": topicOnly} {
		told := notificationsOf(t, a, who)
		if len(told) != 1 {
			t.Errorf("%s was told %d times, want once", name, len(told))
			continue
		}
		if told[0].Kind != models.NotifyTopicUpdate || told[0].Title != about.Title || told[0].Body != "Rat vertagt" {
			t.Errorf("%s was told %+v", name, told[0])
		}
	}
	if told := notificationsOf(t, a, stranger); len(told) != 0 {
		t.Errorf("somebody who follows nothing was told %d things", len(told))
	}

	// Fixing a typo in something already published is not news.
	publish.Body.Body = "Die Abstimmung ist auf den 12. November verschoben."
	if _, err := a.staffSaveUpdate(me, publish); err != nil {
		t.Fatalf("staffSaveUpdate: %v", err)
	}
	if told := notificationsOf(t, a, both); len(told) != 1 {
		t.Errorf("an edit told a follower again: %d notifications", len(told))
	}

	// And the reader's own feed is exactly what they follow.
	feed, err := a.listUpdates(as(topicOnly), &UpdateListInput{Following: true})
	if err != nil {
		t.Fatalf("listUpdates: %v", err)
	}
	if len(feed.Body.Updates) != 1 {
		t.Errorf("the follower's feed has %d updates, want 1", len(feed.Body.Updates))
	}
	empty, err := a.listUpdates(as(stranger), &UpdateListInput{Following: true})
	if err != nil {
		t.Fatalf("listUpdates: %v", err)
	}
	if len(empty.Body.Updates) != 0 {
		t.Errorf("somebody who follows nothing has %d updates in their own feed", len(empty.Body.Updates))
	}
	if _, err := a.listUpdates(anonymous(), &UpdateListInput{Following: true}); statusOf(err) != http.StatusUnauthorized {
		t.Errorf("an anonymous reader asking for their own feed: %v, want 401", err)
	}
}

// TestPeopleWhoWereComingAreToldWhenThePlanChanges, and only then.
func TestPeopleWhoWereComingAreToldWhenThePlanChanges(t *testing.T) {
	a := newAPI(t)
	owner := founded(t, a, "Bündnis Düsseldorf", "duesseldorf")
	me := editor("Dominique", adminsOf("duesseldorf"))
	starts := time.Now().Add(72 * time.Hour).Truncate(time.Minute)
	demo := announced(t, a, me, owner.ID, "Demo vor dem Rathaus", starts)

	coming := reader(t, a, "Camille")
	watching := reader(t, a, "Alex") // follows, but never said they were coming
	if _, err := a.participate(as(coming), &ActionIDInput{ID: demo.ID}); err != nil {
		t.Fatalf("participate: %v", err)
	}
	if _, err := a.follow(as(watching), &FollowInput{Target: "collective", ID: owner.ID}); err != nil {
		t.Fatalf("follow: %v", err)
	}

	save := func(change func(*ActionFields)) {
		t.Helper()
		in := &SaveActionInput{ID: demo.ID}
		in.Body.Title, in.Body.Kind = demo.Title, demo.Kind
		in.Body.StartsAt = starts.Format(time.RFC3339)
		in.Body.Status = "published"
		in.Body.Latitude, in.Body.Longitude, in.Body.Zoom, in.Body.Place = demo.Latitude, demo.Longitude, 18, demo.Place
		in.Body.Description = "Bringt Transparente mit."
		change(&in.Body)
		if _, err := a.staffSaveAction(me, in); err != nil {
			t.Fatalf("staffSaveAction: %v", err)
		}
	}

	// A reworded description is not a reason to make anybody's phone buzz.
	save(func(fields *ActionFields) { fields.Description = "Bringt Transparente und Trillerpfeifen mit." })
	if told := notificationsOf(t, a, coming); len(told) != 0 {
		t.Fatalf("a reworded description told %d people", len(told))
	}

	// Moved by an hour: the plan somebody made is no longer the one on offer.
	save(func(fields *ActionFields) { fields.StartsAt = starts.Add(time.Hour).Format(time.RFC3339) })
	told := notificationsOf(t, a, coming)
	if len(told) != 1 || told[0].Kind != models.NotifyActionChanged {
		t.Fatalf("after moving it: %+v, want one notification that it changed", told)
	}
	if !strings.Contains(told[0].Title, demo.Title) || !strings.HasPrefix(told[0].Title, "Geändert") {
		t.Errorf("title = %q, want the installation's own word and the action's title", told[0].Title)
	}

	// Called off.
	save(func(fields *ActionFields) {
		fields.StartsAt = starts.Add(time.Hour).Format(time.RFC3339)
		fields.Cancelled = true
	})
	told = notificationsOf(t, a, coming)
	if len(told) != 2 {
		t.Fatalf("after cancelling: %d notifications, want 2", len(told))
	}
	// Newest first.
	if told[0].Kind != models.NotifyActionCancelled || !strings.HasPrefix(told[0].Title, "Abgesagt") {
		t.Errorf("the cancellation read %+v", told[0])
	}

	// Somebody who only follows the collective was told it was announced and
	// nothing since: they made no plan for this to disturb.
	if got := notificationsOf(t, a, watching); len(got) != 0 {
		t.Errorf("a follower who was not coming was told %d things about the change", len(got))
	}

	// It stays in the calendar, saying so — it does not vanish.
	listing, err := a.listActions(anonymous(), &ActionListInput{})
	if err != nil {
		t.Fatalf("listActions: %v", err)
	}
	if len(listing.Body.Actions) != 1 || !listing.Body.Actions[0].Cancelled {
		t.Errorf("a cancelled action is not in the calendar as cancelled: %+v", listing.Body.Actions)
	}

	// And nobody new can say they are coming to it.
	if _, err := a.participate(as(watching), &ActionIDInput{ID: demo.ID}); statusOf(err) != http.StatusConflict {
		t.Errorf("attending a cancelled action: %v, want 409", err)
	}
}

// TestAnActionCanOnlyPointAtItsOwnCollectivesTopic: linking to another's would
// put this action on a page its authors do not control.
func TestAnActionCanOnlyPointAtItsOwnCollectivesTopic(t *testing.T) {
	a := newAPI(t)
	mine := founded(t, a, "Bündnis Düsseldorf", "duesseldorf")
	theirs := founded(t, a, "Bündnis Köln", "koeln")
	theirTopic := raised(t, a, editor("Camille", adminsOf("koeln")), theirs.ID, "Schwimmbad schließt")
	myTopic := raised(t, a, editor("Dominique", adminsOf("duesseldorf")), mine.ID, "Haushalt")

	in := &CreateActionInput{ID: mine.ID}
	in.Body.Title, in.Body.Kind = "Demo", "demonstration"
	in.Body.StartsAt = time.Now().Add(24 * time.Hour).Format(time.RFC3339)

	in.Body.TopicID = theirTopic.ID
	if _, err := a.staffCreateAction(editor("Dominique", adminsOf("duesseldorf")), in); statusOf(err) != http.StatusUnprocessableEntity {
		t.Errorf("an action about another collective's topic: %v, want 422", err)
	}

	in.Body.TopicID = myTopic.ID
	out, err := a.staffCreateAction(editor("Dominique", adminsOf("duesseldorf")), in)
	if err != nil {
		t.Fatalf("an action about its own collective's topic: %v", err)
	}
	if out.Body.TopicID != myTopic.ID {
		t.Errorf("topic = %q, want the link kept", out.Body.TopicID)
	}
}

// TestWhatAnEditorWritesIsCheckedLikeAnybodysWriting: one compromised editor
// account is all it takes for "written by staff" to stop meaning "written in
// good faith", and the text here leaves this site — into feed readers,
// calendars and lock screens that do not escape the way our templates do.
func TestWhatAnEditorWritesIsCheckedLikeAnybodysWriting(t *testing.T) {
	a := newAPI(t)
	owner := founded(t, a, "Bündnis Düsseldorf", "duesseldorf")
	me := editor("Dominique", adminsOf("duesseldorf"))

	valid := func() *CreateTopicInput {
		in := &CreateTopicInput{ID: owner.ID}
		in.Body.Title, in.Body.Kind, in.Body.Level = "100 Millionen weniger", "cut", "municipal"
		return in
	}

	for name, break_ := range map[string]func(*TopicFields){
		"markup in the title":       func(f *TopicFields) { f.Title = `Haushalt <script>alert(1)</script>` },
		"a handler in the body":     func(f *TopicFields) { f.Body = `<img src=x onerror=alert(1)>` },
		"a script link as a source": func(f *TopicFields) { f.SourceURL = "javascript:alert(1)" },
		"a source that is no link":  func(f *TopicFields) { f.SourceURL = "siehe Ratsinformationssystem" },
		"no title":                  func(f *TopicFields) { f.Title = "   " },
		"an unknown kind":           func(f *TopicFields) { f.Kind = "rumour" },
		"an unknown level":          func(f *TopicFields) { f.Level = "galactic" },
		"a negative amount":         func(f *TopicFields) { f.Amount = -100_000_000 },
		"an unknown status":         func(f *TopicFields) { f.Status = "secret" },
		"a title that is too long":  func(f *TopicFields) { f.Title = strings.Repeat("ä", maxTitleRunes+1) },
	} {
		in := valid()
		break_(&in.Body)
		if _, err := a.staffCreateTopic(me, in); statusOf(err) != http.StatusUnprocessableEntity {
			t.Errorf("%s: %v, want 422", name, err)
		}
	}

	// What is accepted is stored cleaned, not merely accepted.
	in := valid()
	in.Body.Title = "  100 Millionen\nweniger  "
	in.Body.Body = "Erster Absatz.\r\n\r\nZweiter Absatz."
	in.Body.Amount = 100_000_000
	in.Body.SourceURL = "https://ratsinfo.example/vorlage/2026-0412"
	out, err := a.staffCreateTopic(me, in)
	if err != nil {
		t.Fatalf("staffCreateTopic: %v", err)
	}
	if out.Body.Title != "100 Millionen weniger" {
		t.Errorf("title = %q, want one clean line", out.Body.Title)
	}
	if out.Body.Body != "Erster Absatz.\n\nZweiter Absatz." {
		t.Errorf("body = %q, want the paragraphs kept", out.Body.Body)
	}
	if out.Body.Amount != 100_000_000 {
		t.Errorf("amount = %d", out.Body.Amount)
	}
}

// TestEveryWriteIsNamedInTheLog: content is published without review, and
// this is the other half of that bargain.
func TestEveryWriteIsNamedInTheLog(t *testing.T) {
	a := newAPI(t)
	owner := founded(t, a, "Bündnis Düsseldorf", "duesseldorf")
	other := founded(t, a, "Bündnis Köln", "koeln")
	me := editor("Dominique", adminsOf("duesseldorf"))

	about := raised(t, a, me, owner.ID, "100 Millionen weniger")
	raised(t, a, editor("Camille", adminsOf("koeln")), other.ID, "Schwimmbad schließt")
	if _, err := a.staffDeleteTopic(me, &TopicIDInput{ID: about.ID}); err != nil {
		t.Fatalf("staffDeleteTopic: %v", err)
	}

	// An editor reads their own collective's history.
	mine, err := a.staffAudit(me, &AuditInput{})
	if err != nil {
		t.Fatalf("staffAudit: %v", err)
	}
	var verbs []string
	for _, entry := range mine.Body.Entries {
		if entry.CollectiveID != owner.ID {
			t.Errorf("an editor was shown another collective's history: %+v", entry)
		}
		if entry.SubjectType == "topic" {
			verbs = append(verbs, entry.Action)
			if entry.ActorName != "Dominique" || entry.Actor != "sub-Dominique" {
				t.Errorf("entry names %q / %q, want who did it", entry.ActorName, entry.Actor)
			}
			// Readable after the topic itself is gone.
			if entry.Summary != "100 Millionen weniger" {
				t.Errorf("summary = %q, want what the thing was called", entry.Summary)
			}
		}
	}
	// Newest first: deleted, published, created.
	if strings.Join(verbs, ",") != "delete,publish,create" {
		t.Errorf("the topic's history reads %v, want delete, publish, create", verbs)
	}

	// An administrator reads everything.
	all, err := a.staffAudit(admin(), &AuditInput{})
	if err != nil {
		t.Fatalf("staffAudit: %v", err)
	}
	if all.Body.Total <= mine.Body.Total {
		t.Errorf("an administrator sees %d entries and an editor %d", all.Body.Total, mine.Body.Total)
	}

	// Somebody in no group reads nothing.
	none, err := a.staffAudit(editor("Nobody"), &AuditInput{})
	if err != nil {
		t.Fatalf("staffAudit: %v", err)
	}
	if none.Body.Total != 0 {
		t.Errorf("somebody in no group was shown %d audit entries", none.Body.Total)
	}
}

// TestALogoIsAnImageAndNothingElse.
func TestALogoIsAnImageAndNothingElse(t *testing.T) {
	a := newAPI(t)
	owner := founded(t, a, "Bündnis Düsseldorf", "duesseldorf")
	me := editor("Dominique", adminsOf("duesseldorf"))
	png := []byte("\x89PNG\r\n\x1a\n not really a picture")

	for name, in := range map[string]*LogoInput{
		"a web page":  {ID: owner.ID, ContentType: "text/html", RawBody: []byte("<html>")},
		"a script":    {ID: owner.ID, ContentType: "application/javascript", RawBody: []byte("alert(1)")},
		"nothing":     {ID: owner.ID, ContentType: "image/png", RawBody: nil},
		"a too-large": {ID: owner.ID, ContentType: "image/png", RawBody: make([]byte, models.MaxMediaBytes+1)},
	} {
		if _, err := a.staffPutCollectiveLogo(me, in); statusOf(err) != http.StatusUnprocessableEntity {
			t.Errorf("uploading %s: %v, want 422", name, err)
		}
	}

	first, err := a.staffPutCollectiveLogo(me, &LogoInput{
		ID: owner.ID, ContentType: "image/png; charset=binary", RawBody: png,
	})
	if err != nil {
		t.Fatalf("staffPutCollectiveLogo: %v", err)
	}

	served, err := a.getMedia(anonymous(), &MediaIDInput{ID: first.Body.LogoID})
	if err != nil {
		t.Fatalf("getMedia: %v", err)
	}
	if string(served.Body) != string(png) || served.ContentType != "image/png" {
		t.Errorf("served %q as %q", served.Body, served.ContentType)
	}
	// What stops an uploaded SVG being code execution on this origin.
	if !strings.Contains(served.ContentSecurityPolicy, "sandbox") || served.ContentTypeOptions != "nosniff" {
		t.Errorf("an image is served without its policy: %q / %q",
			served.ContentSecurityPolicy, served.ContentTypeOptions)
	}

	// Replacing it removes the old one, row and bytes: an orphaned blob is
	// invisible and never reclaimed.
	second, err := a.staffPutCollectiveLogo(me, &LogoInput{
		ID: owner.ID, ContentType: "image/svg+xml", RawBody: []byte("<svg xmlns='http://www.w3.org/2000/svg'/>"),
	})
	if err != nil {
		t.Fatalf("replacing the logo: %v", err)
	}
	if _, err := a.getMedia(anonymous(), &MediaIDInput{ID: first.Body.LogoID}); statusOf(err) != http.StatusNotFound {
		t.Errorf("the replaced logo still answers: %v", err)
	}
	if _, err := a.store.Media(context.Background(), first.Body.LogoID); !errors.Is(err, store.ErrMediaNotFound) {
		t.Errorf("the replaced logo's record survived: %v", err)
	}

	// Another collective's editor cannot touch it.
	if _, err := a.staffDeleteCollectiveLogo(editor("Camille", adminsOf("koeln")),
		&CollectiveIDInput{ID: owner.ID}); statusOf(err) != http.StatusNotFound {
		t.Errorf("another editor removing the logo: %v, want 404", err)
	}

	if _, err := a.staffDeleteCollectiveLogo(me, &CollectiveIDInput{ID: owner.ID}); err != nil {
		t.Fatalf("staffDeleteCollectiveLogo: %v", err)
	}
	if _, err := a.getMedia(anonymous(), &MediaIDInput{ID: second.Body.LogoID}); statusOf(err) != http.StatusNotFound {
		t.Errorf("a removed logo still answers: %v", err)
	}
}

// TestTheMapCountsEverythingWhateverItDraws.
func TestTheMapCountsEverythingWhateverItDraws(t *testing.T) {
	a := newAPI(t)
	owner := founded(t, a, "Bündnis NRW", "nrw")
	me := editor("Dominique", adminsOf("nrw"))

	// One topic in Düsseldorf with a figure, one in Berlin with another.
	duesseldorf := &CreateTopicInput{ID: owner.ID}
	duesseldorf.Body.Title, duesseldorf.Body.Kind, duesseldorf.Body.Level = "Düsseldorf: Haushalt", "cut", "municipal"
	duesseldorf.Body.Status, duesseldorf.Body.Amount = "published", 100_000_000
	duesseldorf.Body.Latitude, duesseldorf.Body.Longitude, duesseldorf.Body.Zoom = 51.2277, 6.7735, 11
	if _, err := a.staffCreateTopic(me, duesseldorf); err != nil {
		t.Fatalf("staffCreateTopic: %v", err)
	}

	berlin := &CreateTopicInput{ID: owner.ID}
	berlin.Body.Title, berlin.Body.Kind, berlin.Body.Level = "Berlin: Kita", "cut", "state"
	berlin.Body.Status, berlin.Body.Amount = "published", 40_000_000
	berlin.Body.Latitude, berlin.Body.Longitude, berlin.Body.Zoom = 52.52, 13.405, 11
	if _, err := a.staffCreateTopic(me, berlin); err != nil {
		t.Fatalf("staffCreateTopic: %v", err)
	}

	// A federal reform pinned on a view of the whole country: a name, and no
	// point on any map.
	federal := &CreateTopicInput{ID: owner.ID}
	federal.Body.Title, federal.Body.Kind, federal.Body.Level = "Bürgergeld", "reform", "federal"
	federal.Body.Status = "published"
	federal.Body.Latitude, federal.Body.Longitude, federal.Body.Zoom = 51.1657, 10.4515, 5
	federal.Body.Place = "Deutschland"
	placed, err := a.staffCreateTopic(me, federal)
	if err != nil {
		t.Fatalf("staffCreateTopic: %v", err)
	}
	if placed.Body.Latitude != 0 || placed.Body.Longitude != 0 || placed.Body.Place != "Deutschland" {
		t.Errorf("a country view was stored as a point: %+v", placed.Body)
	}

	// A draft with a figure must not leak into the total.
	draft := &CreateTopicInput{ID: owner.ID}
	draft.Body.Title, draft.Body.Kind, draft.Body.Level, draft.Body.Amount = "Entwurf", "cut", "municipal", 999
	if _, err := a.staffCreateTopic(me, draft); err != nil {
		t.Fatalf("staffCreateTopic: %v", err)
	}

	announced(t, a, me, owner.ID, "Demo Düsseldorf", time.Now().Add(48*time.Hour))
	announced(t, a, me, owner.ID, "Schon vorbei", time.Now().Add(-48*time.Hour))

	// A viewport over Düsseldorf alone.
	view, err := a.readMap(anonymous(), &BoundsInput{Bounds: "51.4,51.1,6.95,6.6"})
	if err != nil {
		t.Fatalf("readMap: %v", err)
	}
	if len(view.Body.Topics) != 1 || view.Body.Topics[0].Title != "Düsseldorf: Haushalt" {
		t.Errorf("drew %d topics, want the one in view", len(view.Body.Topics))
	}
	if len(view.Body.Actions) != 1 || view.Body.Actions[0].Title != "Demo Düsseldorf" {
		t.Errorf("drew %d actions, want the upcoming one in view", len(view.Body.Actions))
	}
	if view.Body.Totals.Topics != 3 {
		t.Errorf("topics total = %d, want all 3 published — a viewport must not shrink the count", view.Body.Totals.Topics)
	}
	if view.Body.Totals.Actions != 1 {
		t.Errorf("actions total = %d, want the 1 that is still to come", view.Body.Totals.Actions)
	}
	if view.Body.Totals.Amount != 140_000_000 {
		t.Errorf("amount = %d, want the two published figures and not the draft's", view.Body.Totals.Amount)
	}
	// A popup shows a summary; the page's worth of text stays on the page.
	if view.Body.Topics[0].Body != "" {
		t.Error("the map carries a topic's whole body for every pin in view")
	}

	// A nonsense viewport is refused rather than read as the whole world.
	if _, err := a.readMap(anonymous(), &BoundsInput{Bounds: "everywhere"}); statusOf(err) != http.StatusUnprocessableEntity {
		t.Errorf("a nonsense viewport: %v, want 422", err)
	}
}
