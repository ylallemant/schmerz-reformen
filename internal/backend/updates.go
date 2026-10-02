package backend

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/cache"
	"github.com/ylallemant/schmerz-reformen/internal/content"
	"github.com/ylallemant/schmerz-reformen/internal/models"
	"github.com/ylallemant/schmerz-reformen/internal/store"
)

func (a *API) registerUpdateRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-updates",
		Method:      http.MethodGet,
		Path:        "/v1/updates",
		Summary:     "Read the feed",
		Description: "News on topics, newest first and by nothing else. Narrow it to one topic " +
			"or one collective; or, signed in, pass `following=true` for what this account " +
			"follows.",
		Tags: []string{"Feed"},
	}, a.listUpdates)

	huma.Register(api, huma.Operation{
		OperationID: "get-update",
		Method:      http.MethodGet,
		Path:        "/v1/updates/{id}",
		Summary:     "Read one update",
		Tags:        []string{"Feed"},
	}, a.getUpdate)

	// --- the console ---

	huma.Register(api, staffOnly(huma.Operation{
		OperationID: "staff-list-updates",
		Method:      http.MethodGet,
		Path:        "/v1/staff/topics/{id}/updates",
		Summary:     "List a topic's updates, at every status",
		Tags:        []string{"Console"},
	}), a.staffListUpdates)

	huma.Register(api, invalidates(cache.Updates)(staffOnly(huma.Operation{
		OperationID: "staff-create-update",
		Method:      http.MethodPost,
		Path:        "/v1/staff/topics/{id}/updates",
		Summary:     "Write an update on a topic",
		Description: "Publishing it tells everybody who follows the topic or its collective, once.",
		Tags:        []string{"Console"},
	})), a.staffCreateUpdate)

	huma.Register(api, staffOnly(huma.Operation{
		OperationID: "staff-get-update",
		Method:      http.MethodGet,
		Path:        "/v1/staff/updates/{id}",
		Summary:     "Read one update as its editors see it",
		Tags:        []string{"Console"},
	}), a.staffGetUpdate)

	huma.Register(api, invalidates(cache.Updates)(staffOnly(huma.Operation{
		OperationID: "staff-save-update",
		Method:      http.MethodPut,
		Path:        "/v1/staff/updates/{id}",
		Summary:     "Change an update",
		Tags:        []string{"Console"},
	})), a.staffSaveUpdate)

	huma.Register(api, invalidates(cache.Updates)(staffOnly(huma.Operation{
		OperationID: "staff-delete-update",
		Method:      http.MethodDelete,
		Path:        "/v1/staff/updates/{id}",
		Summary:     "Delete an update",
		Tags:        []string{"Console"},
	})), a.staffDeleteUpdate)
}

// UpdateItem is a topic update on the wire.
type UpdateItem struct {
	ID string `json:"id"`

	TopicID string    `json:"topic_id"`
	Topic   *TopicRef `json:"topic,omitempty"`

	CollectiveID string         `json:"collective_id"`
	Collective   *CollectiveRef `json:"collective,omitempty"`

	Title string `json:"title"`
	Body  string `json:"body"`

	// Excerpt is the body cut to a card, and Truncated says whether it was
	// cut. Derived here rather than by whoever draws the card, so a feed, a
	// map popup and a syndication feed all stop at the same word.
	Excerpt   string `json:"excerpt"`
	Truncated bool   `json:"truncated"`

	SourceURL string `json:"source_url,omitempty"`

	Status      string     `json:"status"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func toUpdateItem(u models.TopicUpdate, topic models.Topic, owner models.Collective) UpdateItem {
	excerpt, truncated := content.Excerpt(u.Body, content.DefaultExcerptRunes)
	return UpdateItem{
		ID:           u.ID,
		TopicID:      u.TopicID,
		Topic:        toTopicRef(topic),
		CollectiveID: u.CollectiveID,
		Collective:   toCollectiveRef(owner),
		Title:        u.Title,
		Body:         u.Body,
		Excerpt:      excerpt,
		Truncated:    truncated,
		SourceURL:    u.SourceURL,
		Status:       string(u.Status),
		PublishedAt:  u.PublishedAt,
		UpdatedAt:    u.UpdatedAt,
	}
}

// UpdateListInput narrows the feed.
type UpdateListInput struct {
	Topic      string `query:"topic" doc:"a topic's identifier"`
	Collective string `query:"collective" doc:"a collective's identifier"`
	Following  bool   `query:"following" doc:"only what the signed-in account follows"`
	Limit      int    `query:"limit" doc:"how many to return; the default is 30"`
	Offset     int    `query:"offset"`
}

// UpdatesOutput is a page of the feed.
type UpdatesOutput struct {
	Body struct {
		Updates []UpdateItem `json:"updates"`
		Total   int64        `json:"total"`
	}
}

// updateListing is updates with the topics and collectives they name.
type updateListing struct {
	updates []models.TopicUpdate
	topics  map[string]models.Topic
	owners  map[string]models.Collective
	total   int64
}

// readUpdates runs a feed query and resolves what each entry refers to, in
// three queries however long the page is.
func (a *API) readUpdates(ctx context.Context, query store.UpdateQuery) (updateListing, error) {
	updates, total, err := a.store.ListUpdates(ctx, query)
	if err != nil {
		return updateListing{}, err
	}

	topicIDs := make([]string, 0, len(updates))
	ownerIDs := make([]string, 0, len(updates))
	seen := map[string]bool{}
	for _, update := range updates {
		ownerIDs = append(ownerIDs, update.CollectiveID)
		if !seen[update.TopicID] {
			seen[update.TopicID] = true
			topicIDs = append(topicIDs, update.TopicID)
		}
	}

	topics, err := a.store.TopicsByID(ctx, topicIDs)
	if err != nil {
		// A feed that cannot name its topics is still a feed.
		log.Error().Err(err).Msg("cannot read the topics of a feed")
		topics = map[string]models.Topic{}
	}
	return updateListing{
		updates: updates, topics: topics, owners: a.ownersOf(ctx, ownerIDs), total: total,
	}, nil
}

// visible drops news about something a reader cannot open: an update whose
// topic, or whose collective, is not public. See actionListing.visible.
func (l updateListing) visible() updateListing {
	kept := l.updates[:0:0]
	for _, update := range l.updates {
		if l.topics[update.TopicID].Status.Public() && l.owners[update.CollectiveID].Status.Public() {
			kept = append(kept, update)
		}
	}
	l.updates = kept
	return l
}

func (l updateListing) items() []UpdateItem {
	items := make([]UpdateItem, 0, len(l.updates))
	for _, update := range l.updates {
		items = append(items, toUpdateItem(update, l.topics[update.TopicID], l.owners[update.CollectiveID]))
	}
	return items
}

func (a *API) listUpdates(ctx context.Context, in *UpdateListInput) (*UpdatesOutput, error) {
	query := store.UpdateQuery{
		TopicID:      in.Topic,
		CollectiveID: in.Collective,
		Statuses:     []models.PublishStatus{models.StatusPublished},
		Page:         store.Page{Limit: in.Limit, Offset: in.Offset},
	}

	var (
		listing updateListing
		err     error
	)
	if in.Following {
		// One reader's feed: never cached, because nothing account-scoped
		// ever is. See internal/cache.
		who, callerErr := mustCaller(ctx)
		if callerErr != nil {
			return nil, callerErr
		}
		following, followErr := a.store.FollowingOf(ctx, who.Account.ID)
		if followErr != nil {
			log.Error().Err(followErr).Msg("cannot read what an account follows")
			return nil, huma.Error500InternalServerError("cannot read the feed")
		}
		query.Following = &following
		listing, err = a.readUpdates(ctx, query)
	} else {
		key := cache.Keyed("updates.list", in.Topic, in.Collective, itoa(in.Limit), itoa(in.Offset))
		listing, err = cache.Fetch(a.cache, key, cache.Updates, func() (updateListing, error) {
			return a.readUpdates(ctx, query)
		})
	}
	if err != nil {
		log.Error().Err(err).Msg("cannot read the feed")
		return nil, huma.Error500InternalServerError("cannot read the feed")
	}

	out := &UpdatesOutput{}
	out.Body.Total = listing.total
	out.Body.Updates = listing.visible().items()
	return out, nil
}

// UpdateIDInput addresses an update.
type UpdateIDInput struct {
	ID string `path:"id"`
}

// UpdateOutput is one update.
type UpdateOutput struct {
	Body UpdateItem
}

func (a *API) getUpdate(ctx context.Context, in *UpdateIDInput) (*UpdateOutput, error) {
	update, err := a.store.Update(ctx, in.ID)
	if errors.Is(err, store.ErrUpdateNotFound) || (err == nil && update.Status != models.StatusPublished) {
		return nil, huma.Error404NotFound("no such update")
	}
	if err != nil {
		log.Error().Err(err).Str("update", in.ID).Msg("cannot read an update")
		return nil, huma.Error500InternalServerError("cannot read the update")
	}

	topic, err := a.store.Topic(ctx, update.TopicID)
	if err != nil || !topic.Status.Public() {
		// News about something that is no longer public has nothing to be
		// news about.
		return nil, huma.Error404NotFound("no such update")
	}
	owner := a.ownersOf(ctx, []string{update.CollectiveID})[update.CollectiveID]
	// And the same for whose it is: content is public only while its
	// collective is.
	if !owner.Status.Public() {
		return nil, huma.Error404NotFound("no such update")
	}
	return &UpdateOutput{Body: toUpdateItem(update, topic, owner)}, nil
}

// --- the console ---

// updateFor loads an update and checks the editor may manage its collective.
func (a *API) updateFor(ctx context.Context, id string) (*staff, models.TopicUpdate, models.Topic, models.Collective, error) {
	fail := func(err error) (*staff, models.TopicUpdate, models.Topic, models.Collective, error) {
		return nil, models.TopicUpdate{}, models.Topic{}, models.Collective{}, err
	}

	if _, err := mustStaff(ctx); err != nil {
		return fail(err)
	}
	update, err := a.store.Update(ctx, id)
	if errors.Is(err, store.ErrUpdateNotFound) {
		return fail(huma.Error404NotFound("no such update"))
	}
	if err != nil {
		log.Error().Err(err).Str("update", id).Msg("cannot read an update")
		return fail(huma.Error500InternalServerError("cannot read the update"))
	}

	who, topic, collective, err := a.topicFor(ctx, update.TopicID)
	if err != nil {
		return fail(huma.Error404NotFound("no such update"))
	}
	return who, update, topic, collective, nil
}

func (a *API) staffListUpdates(ctx context.Context, in *TopicIDInput) (*UpdatesOutput, error) {
	_, topic, collective, err := a.topicFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}

	updates, total, err := a.store.ListUpdates(ctx, store.UpdateQuery{
		TopicID: topic.ID,
		Page:    store.Page{Limit: 200},
	})
	if err != nil {
		log.Error().Err(err).Str("topic", topic.ID).Msg("cannot list updates for the console")
		return nil, huma.Error500InternalServerError("cannot list the updates")
	}

	out := &UpdatesOutput{}
	out.Body.Total = total
	out.Body.Updates = make([]UpdateItem, 0, len(updates))
	for _, update := range updates {
		out.Body.Updates = append(out.Body.Updates, toUpdateItem(update, topic, collective))
	}
	return out, nil
}

func (a *API) staffGetUpdate(ctx context.Context, in *UpdateIDInput) (*UpdateOutput, error) {
	_, update, topic, collective, err := a.updateFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	return &UpdateOutput{Body: toUpdateItem(update, topic, collective)}, nil
}

// UpdateFields is what an editor's form sends for an update.
type UpdateFields struct {
	Title     string `json:"title"`
	Body      string `json:"body"`
	SourceURL string `json:"source_url,omitempty"`
	Status    string `json:"status,omitempty" doc:"draft or published"`
}

func applyUpdateFields(update *models.TopicUpdate, fields UpdateFields) error {
	title, err := cleanLine("the title", fields.Title, maxTitleRunes)
	if err != nil {
		return err
	}
	if title == "" {
		return huma.Error422UnprocessableEntity("an update needs a title")
	}
	body, err := cleanText("the text", fields.Body, maxBodyRunes)
	if err != nil {
		return err
	}
	source, err := cleanURL("the source", fields.SourceURL)
	if err != nil {
		return err
	}

	status, err := nextStatus(fields.Status, update.Status)
	if err != nil {
		return err
	}
	if status == models.StatusArchived {
		// News does not get archived; it gets old, which the date already
		// says. Offering the state would be offering a way to make an update
		// neither visible nor gone.
		return huma.Error422UnprocessableEntity("an update is a draft or it is published")
	}

	update.Title = title
	update.Body = body
	update.SourceURL = source
	update.Status = status
	return nil
}

// CreateUpdateInput is a new update on a topic.
type CreateUpdateInput struct {
	ID   string `path:"id"`
	Body UpdateFields
}

func (a *API) staffCreateUpdate(ctx context.Context, in *CreateUpdateInput) (*UpdateOutput, error) {
	who, topic, collective, err := a.topicFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}

	update := &models.TopicUpdate{TopicID: topic.ID, CollectiveID: collective.ID}
	if err := applyUpdateFields(update, in.Body); err != nil {
		return nil, err
	}

	published, err := a.store.SaveUpdate(ctx, update)
	if err != nil {
		log.Error().Err(err).Str("topic", topic.ID).Msg("cannot create an update")
		return nil, huma.Error500InternalServerError("cannot create the update")
	}

	a.audit(ctx, who, models.AuditCreate, "update", update.ID, collective.ID, update.Title)
	if published {
		a.audit(ctx, who, models.AuditPublish, "update", update.ID, collective.ID, update.Title)
		a.announceUpdate(ctx, *update, topic, collective)
	}
	return &UpdateOutput{Body: toUpdateItem(*update, topic, collective)}, nil
}

// SaveUpdateInput changes an update.
type SaveUpdateInput struct {
	ID   string `path:"id"`
	Body UpdateFields
}

func (a *API) staffSaveUpdate(ctx context.Context, in *SaveUpdateInput) (*UpdateOutput, error) {
	who, update, topic, collective, err := a.updateFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	before := update.Status

	if err := applyUpdateFields(&update, in.Body); err != nil {
		return nil, err
	}

	published, err := a.store.SaveUpdate(ctx, &update)
	if err != nil {
		log.Error().Err(err).Str("update", update.ID).Msg("cannot save an update")
		return nil, huma.Error500InternalServerError("cannot save the update")
	}

	a.audit(ctx, who, statusAction(before, update.Status), "update", update.ID, collective.ID, update.Title)
	if published {
		a.announceUpdate(ctx, update, topic, collective)
	}
	return &UpdateOutput{Body: toUpdateItem(update, topic, collective)}, nil
}

func (a *API) staffDeleteUpdate(ctx context.Context, in *UpdateIDInput) (*DoneOutput, error) {
	who, update, _, collective, err := a.updateFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if err := a.store.DeleteUpdate(ctx, update.ID); err != nil && !errors.Is(err, store.ErrUpdateNotFound) {
		log.Error().Err(err).Str("update", update.ID).Msg("cannot delete an update")
		return nil, huma.Error500InternalServerError("cannot delete the update")
	}

	a.audit(ctx, who, models.AuditDelete, "update", update.ID, collective.ID, update.Title)
	return done(), nil
}

// announceUpdate tells the people following a topic, or its collective, that
// there is news — once each, however many ways they follow it.
//
// Only when the news can actually be read: an update published on a draft
// topic waits with it, and says nothing to anybody.
func (a *API) announceUpdate(ctx context.Context, update models.TopicUpdate, topic models.Topic, collective models.Collective) {
	if topic.Status != models.StatusPublished || collective.Status != models.StatusPublished {
		return
	}
	accounts, err := a.store.FollowersOf(ctx, collective.ID, topic.ID)
	if err != nil {
		log.Error().Err(err).Str("update", update.ID).Msg("cannot read who to tell about an update")
		return
	}
	a.tell(ctx, accounts, Announcement{
		Kind:        models.NotifyTopicUpdate,
		Title:       topic.Title,
		Body:        update.Title,
		SubjectType: "update",
		SubjectID:   update.ID,
		Path:        "/updates/" + update.ID,
	})
}
