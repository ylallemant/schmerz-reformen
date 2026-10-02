package backend

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/cache"
	"github.com/ylallemant/schmerz-reformen/internal/models"
	"github.com/ylallemant/schmerz-reformen/internal/store"
)

func (a *API) registerActionRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-actions",
		Method:      http.MethodGet,
		Path:        "/v1/actions",
		Summary:     "Read the calendar",
		Description: "Published actions in the order they happen. `from` and `to` bound the " +
			"start time; with neither, the listing starts now. Signed in, `mine=true` returns " +
			"the actions this account said it is coming to.",
		Tags: []string{"Calendar"},
	}, a.listActions)

	huma.Register(api, huma.Operation{
		OperationID: "get-action",
		Method:      http.MethodGet,
		Path:        "/v1/actions/{id}",
		Summary:     "Read one action",
		Tags:        []string{"Calendar"},
	}, a.getAction)

	// --- the console ---

	huma.Register(api, staffOnly(huma.Operation{
		OperationID: "staff-list-actions",
		Method:      http.MethodGet,
		Path:        "/v1/staff/collectives/{id}/actions",
		Summary:     "List a collective's actions, at every status",
		Tags:        []string{"Console"},
	}), a.staffListActions)

	huma.Register(api, invalidates(cache.Actions)(staffOnly(huma.Operation{
		OperationID: "staff-create-action",
		Method:      http.MethodPost,
		Path:        "/v1/staff/collectives/{id}/actions",
		Summary:     "Announce an action",
		Tags:        []string{"Console"},
	})), a.staffCreateAction)

	huma.Register(api, staffOnly(huma.Operation{
		OperationID: "staff-get-action",
		Method:      http.MethodGet,
		Path:        "/v1/staff/actions/{id}",
		Summary:     "Read one action as its editors see it",
		Tags:        []string{"Console"},
	}), a.staffGetAction)

	huma.Register(api, invalidates(cache.Actions)(staffOnly(huma.Operation{
		OperationID: "staff-save-action",
		Method:      http.MethodPut,
		Path:        "/v1/staff/actions/{id}",
		Summary:     "Change an action",
		Description: "Moving a published action in time or place, or calling it off, tells " +
			"everybody who said they were coming.",
		Tags: []string{"Console"},
	})), a.staffSaveAction)

	huma.Register(api, invalidates(cache.Actions)(staffOnly(huma.Operation{
		OperationID: "staff-delete-action",
		Method:      http.MethodDelete,
		Path:        "/v1/staff/actions/{id}",
		Summary:     "Delete an action",
		Description: "For something that should never have been announced — a duplicate, a " +
			"mistake. An action that was announced and is not happening is **cancelled**, " +
			"so that it says so wherever people saw it.",
		Tags: []string{"Console"},
	})), a.staffDeleteAction)
}

// ActionItem is an action on the wire.
type ActionItem struct {
	ID string `json:"id"`

	CollectiveID string         `json:"collective_id"`
	Collective   *CollectiveRef `json:"collective,omitempty"`

	TopicID string    `json:"topic_id,omitempty"`
	Topic   *TopicRef `json:"topic,omitempty"`

	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`

	StartsAt time.Time  `json:"starts_at"`
	EndsAt   *time.Time `json:"ends_at,omitempty"`

	Place     string  `json:"place,omitempty"`
	Latitude  float64 `json:"latitude,omitempty"`
	Longitude float64 `json:"longitude,omitempty"`

	ExternalURL string `json:"external_url,omitempty"`

	Status string `json:"status"`
	// Cancelled is sent explicitly: an action that is off must never read as
	// "nothing said" to a client that reused a struct.
	Cancelled   bool       `json:"cancelled"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`

	// Participants is how many accounts said they are coming.
	Participants int64 `json:"participants"`
	// Participating is whether the signed-in reader did. Explicit, as above.
	Participating bool `json:"participating"`
}

func toActionItem(action models.Action, topic models.Topic, owner models.Collective) ActionItem {
	item := ActionItem{
		ID:           action.ID,
		CollectiveID: action.CollectiveID,
		Collective:   toCollectiveRef(owner),
		TopicID:      action.TopicID,
		Topic:        toTopicRef(topic),
		Kind:         string(action.Kind),
		Title:        action.Title,
		Description:  action.Description,
		StartsAt:     action.StartsAt,
		EndsAt:       action.EndsAt,
		Place:        action.Location.Label,
		ExternalURL:  action.ExternalURL,
		Status:       string(action.Status),
		Cancelled:    action.Cancelled,
		PublishedAt:  action.PublishedAt,
		UpdatedAt:    action.UpdatedAt,
	}
	if action.Location.Placed() {
		item.Latitude, item.Longitude = action.Location.Latitude, action.Location.Longitude
	}
	return item
}

// ActionListInput narrows the calendar.
type ActionListInput struct {
	Collective string `query:"collective" doc:"a collective's identifier"`
	Topic      string `query:"topic" doc:"a topic's identifier"`
	Kind       string `query:"kind" doc:"demonstration, rally, strike, meeting, info, council or other"`
	From       string `query:"from" doc:"RFC 3339; actions starting at or after this. Defaults to now when to is absent too"`
	To         string `query:"to" doc:"RFC 3339; actions starting before this"`
	Bounds     string `query:"bounds" doc:"north,south,east,west — a map viewport"`
	Mine       bool   `query:"mine" doc:"only the actions the signed-in account is coming to"`
	Limit      int    `query:"limit" doc:"how many to return; the default is 200"`
	Offset     int    `query:"offset"`
}

// ActionsOutput is a listing of actions.
type ActionsOutput struct {
	Body struct {
		Actions []ActionItem `json:"actions"`
		Total   int64        `json:"total"`
	}
}

// actionListing is actions with the topics and collectives they name.
type actionListing struct {
	actions []models.Action
	topics  map[string]models.Topic
	owners  map[string]models.Collective
	total   int64
}

// readActions runs a calendar query and resolves what each entry refers to.
func (a *API) readActions(ctx context.Context, query store.ActionQuery) (actionListing, error) {
	actions, total, err := a.store.ListActions(ctx, query)
	if err != nil {
		return actionListing{}, err
	}

	var topicIDs, ownerIDs []string
	seen := map[string]bool{}
	for _, action := range actions {
		ownerIDs = append(ownerIDs, action.CollectiveID)
		if action.TopicID != "" && !seen[action.TopicID] {
			seen[action.TopicID] = true
			topicIDs = append(topicIDs, action.TopicID)
		}
	}

	topics, err := a.store.TopicsByID(ctx, topicIDs)
	if err != nil {
		log.Error().Err(err).Msg("cannot read the topics of a calendar")
		topics = map[string]models.Topic{}
	}
	return actionListing{
		actions: actions, topics: topics, owners: a.ownersOf(ctx, ownerIDs), total: total,
	}, nil
}

// items renders a listing for one reader.
//
// The counts and the reader's own answers are added here, on top of rows that
// may have come out of the cache, and are never stored with them: the first is
// cheap and should be exact — somebody who just pressed "I'm coming" is looking
// straight at the number — and the second is one person's and must never be
// served to another.
func (a *API) actionItems(ctx context.Context, listing actionListing) []ActionItem {
	ids := make([]string, 0, len(listing.actions))
	for _, action := range listing.actions {
		ids = append(ids, action.ID)
	}

	counts, err := a.store.ParticipantCounts(ctx, ids)
	if err != nil {
		log.Error().Err(err).Msg("cannot count the people coming to actions")
		counts = map[string]int64{}
	}

	coming := map[string]bool{}
	if who := callerOf(ctx); who != nil {
		mine, err := a.store.ParticipationsOf(ctx, who.Account.ID)
		if err != nil {
			log.Error().Err(err).Msg("cannot read what an account is coming to")
		}
		for _, id := range mine {
			coming[id] = true
		}
	}

	items := make([]ActionItem, 0, len(listing.actions))
	for _, action := range listing.actions {
		// The topic is named only while it is public: an action may outlive
		// the page it was about, and a link to a draft is a dead link.
		topic := listing.topics[action.TopicID]
		if !topic.Status.Public() {
			topic = models.Topic{}
		}
		item := toActionItem(action, topic, listing.owners[action.CollectiveID])
		item.Participants = counts[action.ID]
		item.Participating = coming[action.ID]
		items = append(items, item)
	}
	return items
}

// parseInstant reads an optional RFC 3339 time.
func parseInstant(field, value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, huma.Error422UnprocessableEntity(
			field + " must be a time such as 2026-10-03T14:00:00+02:00")
	}
	return parsed, nil
}

func (a *API) listActions(ctx context.Context, in *ActionListInput) (*ActionsOutput, error) {
	query := store.ActionQuery{
		CollectiveID: in.Collective,
		TopicID:      in.Topic,
		Statuses:     []models.PublishStatus{models.StatusPublished},
		Page:         store.Page{Limit: in.Limit, Offset: in.Offset},
	}
	if in.Kind != "" {
		kind := models.ActionKind(in.Kind)
		if !kind.Valid() {
			return nil, huma.Error422UnprocessableEntity("unknown kind of action")
		}
		query.Kinds = []models.ActionKind{kind}
	}

	var err error
	if query.From, err = parseInstant("from", in.From); err != nil {
		return nil, err
	}
	if query.To, err = parseInstant("to", in.To); err != nil {
		return nil, err
	}
	// With no window at all the calendar starts now. Rounded to the minute so
	// that a moving "now" does not make every request a different cache key —
	// a calendar that is a minute stale about what has already started is not
	// stale in any way a reader could notice.
	rounded := ""
	if query.From.IsZero() && query.To.IsZero() {
		query.From = time.Now().UTC().Truncate(time.Minute)
		rounded = query.From.Format(time.RFC3339)
	}

	if query.Bounds, err = optionalBounds(in.Bounds); err != nil {
		return nil, err
	}

	var listing actionListing
	if in.Mine {
		who, callerErr := mustCaller(ctx)
		if callerErr != nil {
			return nil, callerErr
		}
		ids, idsErr := a.store.ParticipationsOf(ctx, who.Account.ID)
		if idsErr != nil {
			log.Error().Err(idsErr).Msg("cannot read what an account is coming to")
			return nil, huma.Error500InternalServerError("cannot read the calendar")
		}
		// Never nil: nil means "no restriction", and somebody coming to
		// nothing has an empty calendar, not everybody's.
		query.IDs = append([]string{}, ids...)
		listing, err = a.readActions(ctx, query)
	} else {
		key := cache.Keyed("actions.list", in.Collective, in.Topic, in.Kind,
			in.From, in.To, rounded, in.Bounds, itoa(in.Limit), itoa(in.Offset))
		listing, err = cache.Fetch(a.cache, key, cache.Actions, func() (actionListing, error) {
			return a.readActions(ctx, query)
		})
	}
	if err != nil {
		log.Error().Err(err).Msg("cannot read the calendar")
		return nil, huma.Error500InternalServerError("cannot read the calendar")
	}

	out := &ActionsOutput{}
	out.Body.Total = listing.total
	out.Body.Actions = a.actionItems(ctx, listing.visible())
	return out, nil
}

// visible drops what a reader may not see because of whose it is: an action
// published by a collective that is itself still a draft.
//
// The action's own status is the query's business. This is the other half —
// content is public only while its collective is — and it is applied to the
// listing rather than joined into the query so that a cached listing and a
// fresh one go through the same door.
func (l actionListing) visible() actionListing {
	kept := l.actions[:0:0]
	for _, action := range l.actions {
		if l.owners[action.CollectiveID].Status.Public() {
			kept = append(kept, action)
		}
	}
	l.actions = kept
	return l
}

// ActionOutput is one action.
type ActionOutput struct {
	Body ActionItem
}

func (a *API) getAction(ctx context.Context, in *ActionIDInput) (*ActionOutput, error) {
	action, err := a.store.Action(ctx, in.ID)
	if errors.Is(err, store.ErrActionNotFound) || (err == nil && !action.Status.Public()) {
		return nil, huma.Error404NotFound("no such action")
	}
	if err != nil {
		log.Error().Err(err).Str("action", in.ID).Msg("cannot read an action")
		return nil, huma.Error500InternalServerError("cannot read the action")
	}

	listing := actionListing{actions: []models.Action{action}}
	listing.owners = a.ownersOf(ctx, []string{action.CollectiveID})
	if !listing.owners[action.CollectiveID].Status.Public() {
		return nil, huma.Error404NotFound("no such action")
	}
	if action.TopicID != "" {
		if topics, err := a.store.TopicsByID(ctx, []string{action.TopicID}); err == nil {
			listing.topics = topics
		}
	}
	return &ActionOutput{Body: a.actionItems(ctx, listing)[0]}, nil
}

// --- the console ---

// actionFor loads an action and checks the editor may manage its collective.
func (a *API) actionFor(ctx context.Context, id string) (*staff, models.Action, models.Collective, error) {
	if _, err := mustStaff(ctx); err != nil {
		return nil, models.Action{}, models.Collective{}, err
	}

	action, err := a.store.Action(ctx, id)
	if errors.Is(err, store.ErrActionNotFound) {
		return nil, models.Action{}, models.Collective{}, huma.Error404NotFound("no such action")
	}
	if err != nil {
		log.Error().Err(err).Str("action", id).Msg("cannot read an action")
		return nil, models.Action{}, models.Collective{}, huma.Error500InternalServerError("cannot read the action")
	}

	who, collective, err := a.collectiveFor(ctx, action.CollectiveID)
	if err != nil {
		return nil, models.Action{}, models.Collective{}, huma.Error404NotFound("no such action")
	}
	return who, action, collective, nil
}

func (a *API) staffListActions(ctx context.Context, in *CollectiveIDInput) (*ActionsOutput, error) {
	_, collective, err := a.collectiveFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}

	// Latest first: an editor opening this list is nearly always looking for
	// what is coming, and the past is what they scroll down to.
	listing, err := a.readActions(ctx, store.ActionQuery{
		CollectiveID: collective.ID,
		Descending:   true,
		Page:         store.Page{Limit: 1000},
	})
	if err != nil {
		log.Error().Err(err).Str("collective", collective.ID).Msg("cannot list actions for the console")
		return nil, huma.Error500InternalServerError("cannot list the actions")
	}

	out := &ActionsOutput{}
	out.Body.Total = listing.total
	out.Body.Actions = a.actionItems(ctx, listing)
	return out, nil
}

func (a *API) staffGetAction(ctx context.Context, in *ActionIDInput) (*ActionOutput, error) {
	_, action, collective, err := a.actionFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}

	listing := actionListing{
		actions: []models.Action{action},
		owners:  map[string]models.Collective{collective.ID: collective},
	}
	return &ActionOutput{Body: a.actionItems(ctx, listing)[0]}, nil
}

// ActionFields is what an editor's form sends for an action.
type ActionFields struct {
	Kind        string `json:"kind" doc:"demonstration, rally, strike, meeting, info, council or other"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`

	TopicID string `json:"topic_id,omitempty" doc:"the topic it is about, if any — one of the same collective's"`

	StartsAt string `json:"starts_at" doc:"RFC 3339, with the offset of where it happens"`
	EndsAt   string `json:"ends_at,omitempty" doc:"RFC 3339; empty for no stated end"`

	ExternalURL string `json:"external_url,omitempty"`
	Status      string `json:"status,omitempty" doc:"draft or published"`
	Cancelled   bool   `json:"cancelled,omitempty"`

	PlaceInput
}

func (a *API) applyActionFields(ctx context.Context, action *models.Action, fields ActionFields) error {
	title, err := cleanLine("the title", fields.Title, maxTitleRunes)
	if err != nil {
		return err
	}
	if title == "" {
		return huma.Error422UnprocessableEntity("an action needs a title")
	}
	description, err := cleanText("the description", fields.Description, maxBodyRunes)
	if err != nil {
		return err
	}
	external, err := cleanURL("the link", fields.ExternalURL)
	if err != nil {
		return err
	}

	kind := models.ActionKind(strings.TrimSpace(fields.Kind))
	if !kind.Valid() {
		return huma.Error422UnprocessableEntity("say what kind of action this is")
	}

	starts, err := parseInstant("the start", fields.StartsAt)
	if err != nil {
		return err
	}
	if starts.IsZero() {
		return huma.Error422UnprocessableEntity("an action needs a date and a time")
	}
	ends, err := parseInstant("the end", fields.EndsAt)
	if err != nil {
		return err
	}
	if !ends.IsZero() && !ends.After(starts) {
		return huma.Error422UnprocessableEntity("the end is not after the start")
	}

	status, err := nextStatus(fields.Status, action.Status)
	if err != nil {
		return err
	}
	if status == models.StatusArchived {
		// The past archives an action by itself. What an editor needs instead
		// is "cancelled", which is a different fact and has its own field.
		return huma.Error422UnprocessableEntity("an action is a draft or it is published")
	}

	topicID := strings.TrimSpace(fields.TopicID)
	if topicID != "" {
		topic, err := a.store.Topic(ctx, topicID)
		// Another collective's topic is refused exactly as one that does not
		// exist: linking to it would put this action on a page its authors do
		// not control.
		if errors.Is(err, store.ErrTopicNotFound) || (err == nil && topic.CollectiveID != action.CollectiveID) {
			return huma.Error422UnprocessableEntity("that topic is not one of this collective's")
		}
		if err != nil {
			log.Error().Err(err).Str("topic", topicID).Msg("cannot read the topic of an action")
			return huma.Error500InternalServerError("cannot save the action")
		}
	}

	location, err := a.resolvePlace(ctx, fields.PlaceInput, action.Location)
	if err != nil {
		return err
	}

	action.Kind = kind
	action.Title = title
	action.Description = description
	action.TopicID = topicID
	action.StartsAt = starts.UTC()
	action.EndsAt = nil
	if !ends.IsZero() {
		utc := ends.UTC()
		action.EndsAt = &utc
	}
	action.ExternalURL = external
	action.Status = status
	action.Cancelled = fields.Cancelled
	action.Location = location
	return nil
}

// CreateActionInput is a new action for a collective.
type CreateActionInput struct {
	ID   string `path:"id"`
	Body ActionFields
}

func (a *API) staffCreateAction(ctx context.Context, in *CreateActionInput) (*ActionOutput, error) {
	who, collective, err := a.collectiveFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}

	action := &models.Action{CollectiveID: collective.ID}
	if err := a.applyActionFields(ctx, action, in.Body); err != nil {
		return nil, err
	}

	published, err := a.store.SaveAction(ctx, action)
	if err != nil {
		log.Error().Err(err).Str("collective", collective.ID).Msg("cannot create an action")
		return nil, huma.Error500InternalServerError("cannot create the action")
	}

	a.audit(ctx, who, models.AuditCreate, "action", action.ID, collective.ID, action.Title)
	if published {
		a.audit(ctx, who, models.AuditPublish, "action", action.ID, collective.ID, action.Title)
		a.announceAction(ctx, *action, collective)
	}
	return a.staffGetAction(ctx, &ActionIDInput{ID: action.ID})
}

// SaveActionInput changes an action.
type SaveActionInput struct {
	ID   string `path:"id"`
	Body ActionFields
}

func (a *API) staffSaveAction(ctx context.Context, in *SaveActionInput) (*ActionOutput, error) {
	who, action, collective, err := a.actionFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	before := action

	if err := a.applyActionFields(ctx, &action, in.Body); err != nil {
		return nil, err
	}

	published, err := a.store.SaveAction(ctx, &action)
	if err != nil {
		log.Error().Err(err).Str("action", action.ID).Msg("cannot save an action")
		return nil, huma.Error500InternalServerError("cannot save the action")
	}

	verb := statusAction(before.Status, action.Status)
	if action.Cancelled && !before.Cancelled {
		verb = models.AuditCancel
	}
	a.audit(ctx, who, verb, "action", action.ID, collective.ID, action.Title)

	switch {
	case published:
		a.announceAction(ctx, action, collective)
	case before.Status == models.StatusPublished && action.Status == models.StatusPublished:
		a.announceChange(ctx, before, action)
	}
	return a.staffGetAction(ctx, &ActionIDInput{ID: action.ID})
}

func (a *API) staffDeleteAction(ctx context.Context, in *ActionIDInput) (*DoneOutput, error) {
	who, action, collective, err := a.actionFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if err := a.store.DeleteAction(ctx, action.ID); err != nil && !errors.Is(err, store.ErrActionNotFound) {
		log.Error().Err(err).Str("action", action.ID).Msg("cannot delete an action")
		return nil, huma.Error500InternalServerError("cannot delete the action")
	}

	a.audit(ctx, who, models.AuditDelete, "action", action.ID, collective.ID, action.Title)
	return done(), nil
}

// announceAction tells the people following a collective, or the topic an
// action is about, that there is something to come to.
func (a *API) announceAction(ctx context.Context, action models.Action, collective models.Collective) {
	if collective.Status != models.StatusPublished || action.Cancelled {
		return
	}
	accounts, err := a.store.FollowersOf(ctx, collective.ID, action.TopicID)
	if err != nil {
		log.Error().Err(err).Str("action", action.ID).Msg("cannot read who to tell about an action")
		return
	}

	body := action.Title
	if action.Location.Label != "" {
		body += " — " + action.Location.Label
	}
	a.tell(ctx, accounts, Announcement{
		Kind:        models.NotifyActionPublished,
		Title:       collective.Name,
		Body:        body,
		SubjectType: "action",
		SubjectID:   action.ID,
		Path:        "/actions/" + action.ID,
	})
}

// announceChange tells the people who said they were coming that the plan
// they made is no longer the one on offer.
//
// Only for what changes whether and where somebody turns up: the time, the
// place, and whether it is happening at all. A reworded description is not a
// reason to make four hundred phones buzz.
func (a *API) announceChange(ctx context.Context, before, after models.Action) {
	cancelled := after.Cancelled && !before.Cancelled
	moved := !after.StartsAt.Equal(before.StartsAt) ||
		after.Location.Latitude != before.Location.Latitude ||
		after.Location.Longitude != before.Location.Longitude ||
		after.Location.Label != before.Location.Label

	if !cancelled && (!moved || after.Cancelled) {
		return
	}

	accounts, err := a.store.ParticipantsOf(ctx, after.ID)
	if err != nil {
		log.Error().Err(err).Str("action", after.ID).Msg("cannot read who is coming to an action")
		return
	}

	announcement := Announcement{
		Kind:        models.NotifyActionChanged,
		Title:       fmt.Sprintf(a.phrases.Moved, after.Title),
		Body:        after.Location.Label,
		SubjectType: "action",
		SubjectID:   after.ID,
		Path:        "/actions/" + after.ID,
	}
	if cancelled {
		announcement.Kind = models.NotifyActionCancelled
		announcement.Title = fmt.Sprintf(a.phrases.Cancelled, after.Title)
	}
	a.tell(ctx, accounts, announcement)
}
