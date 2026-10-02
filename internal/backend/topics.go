package backend

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/cache"
	"github.com/ylallemant/schmerz-reformen/internal/models"
	"github.com/ylallemant/schmerz-reformen/internal/store"
)

func (a *API) registerTopicRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-topics",
		Method:      http.MethodGet,
		Path:        "/v1/topics",
		Summary:     "List the published topics",
		Description: "The cuts, reforms and closures collectives are campaigning on, most " +
			"recently published first. `bounds` narrows what is returned to a map viewport and " +
			"never what `total` counts.",
		Tags: []string{"Topics"},
	}, a.listTopics)

	huma.Register(api, huma.Operation{
		OperationID: "get-topic",
		Method:      http.MethodGet,
		Path:        "/v1/topics/{id}",
		Summary:     "Read one topic",
		Tags:        []string{"Topics"},
	}, a.getTopic)

	// --- the console ---

	huma.Register(api, staffOnly(huma.Operation{
		OperationID: "staff-list-topics",
		Method:      http.MethodGet,
		Path:        "/v1/staff/collectives/{id}/topics",
		Summary:     "List a collective's topics, at every status",
		Tags:        []string{"Console"},
	}), a.staffListTopics)

	huma.Register(api, invalidates(cache.Topics)(staffOnly(huma.Operation{
		OperationID: "staff-create-topic",
		Method:      http.MethodPost,
		Path:        "/v1/staff/collectives/{id}/topics",
		Summary:     "Create a topic",
		Tags:        []string{"Console"},
	})), a.staffCreateTopic)

	huma.Register(api, staffOnly(huma.Operation{
		OperationID: "staff-get-topic",
		Method:      http.MethodGet,
		Path:        "/v1/staff/topics/{id}",
		Summary:     "Read one topic as its editors see it",
		Tags:        []string{"Console"},
	}), a.staffGetTopic)

	huma.Register(api, invalidates(cache.Topics, cache.Updates)(staffOnly(huma.Operation{
		OperationID: "staff-save-topic",
		Method:      http.MethodPut,
		Path:        "/v1/staff/topics/{id}",
		Summary:     "Change a topic",
		Description: "Publishing a topic for the first time tells the collective's followers. " +
			"Editing one that is already public tells nobody: a corrected typo is not news.",
		Tags: []string{"Console"},
	})), a.staffSaveTopic)

	huma.Register(api, invalidates(cache.Topics, cache.Updates, cache.Actions)(staffOnly(huma.Operation{
		OperationID: "staff-delete-topic",
		Method:      http.MethodDelete,
		Path:        "/v1/staff/topics/{id}",
		Summary:     "Delete a topic and its updates",
		Description: "For something that should never have been published. A fight that is over " +
			"is archived instead, so the links people shared keep answering. Actions that were " +
			"about the topic survive and lose the link.",
		Tags: []string{"Console"},
	})), a.staffDeleteTopic)
}

// TopicRef is a topic as something else names it.
type TopicRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
	Place string `json:"place,omitempty"`
}

func toTopicRef(t models.Topic) *TopicRef {
	if t.ID == "" {
		return nil
	}
	return &TopicRef{ID: t.ID, Title: t.Title, Kind: string(t.Kind), Place: t.Location.Label}
}

// TopicItem is a topic on the wire.
type TopicItem struct {
	ID           string         `json:"id"`
	CollectiveID string         `json:"collective_id"`
	Collective   *CollectiveRef `json:"collective,omitempty"`

	Kind  string `json:"kind"`
	Level string `json:"level"`

	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
	Body    string `json:"body,omitempty"`

	// Amount is the money at stake in whole euros; zero means no figure.
	Amount    int64  `json:"amount,omitempty"`
	SourceURL string `json:"source_url,omitempty"`

	Place     string  `json:"place,omitempty"`
	Latitude  float64 `json:"latitude,omitempty"`
	Longitude float64 `json:"longitude,omitempty"`

	Status      string     `json:"status"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`

	Followers int64 `json:"followers"`
	// Following is sent explicitly; see CollectiveItem.Following.
	Following bool `json:"following"`
}

func toTopicItem(t models.Topic, owner models.Collective) TopicItem {
	item := TopicItem{
		ID:           t.ID,
		CollectiveID: t.CollectiveID,
		Collective:   toCollectiveRef(owner),
		Kind:         string(t.Kind),
		Level:        string(t.Level),
		Title:        t.Title,
		Summary:      t.Summary,
		Body:         t.Body,
		Amount:       t.Amount,
		SourceURL:    t.SourceURL,
		Place:        t.Location.Label,
		Status:       string(t.Status),
		PublishedAt:  t.PublishedAt,
		UpdatedAt:    t.UpdatedAt,
	}
	if t.Location.Placed() {
		item.Latitude, item.Longitude = t.Location.Latitude, t.Location.Longitude
	}
	return item
}

// collectivesOfTopics reads the collectives a set of topics belongs to, in one
// query rather than one per topic.
func (a *API) collectivesOfTopics(ctx context.Context, topics map[string]models.Topic) (map[string]models.Collective, error) {
	ids := make([]string, 0, len(topics))
	seen := map[string]bool{}
	for _, topic := range topics {
		if !seen[topic.CollectiveID] {
			seen[topic.CollectiveID] = true
			ids = append(ids, topic.CollectiveID)
		}
	}
	return a.store.CollectivesByID(ctx, ids)
}

// ownersOf reads the collectives named by a list of identifiers, tolerating
// failure: a listing that cannot name whose each entry is still lists them.
func (a *API) ownersOf(ctx context.Context, ids []string) map[string]models.Collective {
	unique := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}

	owners, err := a.store.CollectivesByID(ctx, unique)
	if err != nil {
		log.Error().Err(err).Msg("cannot read the collectives of a listing")
		return map[string]models.Collective{}
	}
	return owners
}

// TopicListInput narrows the public listing.
type TopicListInput struct {
	Collective string `query:"collective" doc:"a collective's identifier"`
	Kind       string `query:"kind" doc:"cut, reform, closure, privatisation or other"`
	Level      string `query:"level" doc:"federal, state or municipal"`
	Bounds     string `query:"bounds" doc:"north,south,east,west — a map viewport"`
	Limit      int    `query:"limit" doc:"how many to return; the default is 200"`
	Offset     int    `query:"offset"`
}

// TopicsOutput is a listing of topics.
type TopicsOutput struct {
	Body struct {
		Topics []TopicItem `json:"topics"`
		Total  int64       `json:"total"`
	}
}

// topicListing is what the cache holds for one question.
type topicListing struct {
	topics []models.Topic
	owners map[string]models.Collective
	total  int64
}

// visible drops what a reader may not see because of whose it is: a topic
// published by a collective that is itself still a draft. See
// actionListing.visible.
func (l topicListing) visible() topicListing {
	kept := l.topics[:0:0]
	for _, topic := range l.topics {
		if l.owners[topic.CollectiveID].Status.Public() {
			kept = append(kept, topic)
		}
	}
	l.topics = kept
	return l
}

// topicQuery turns the public parameters into a store query, refusing a filter
// value that names nothing. An unknown kind answered with an empty list would
// look exactly like "there are none", which is a different and wrong answer.
func topicQuery(in *TopicListInput) (store.TopicQuery, error) {
	query := store.TopicQuery{
		CollectiveID: in.Collective,
		Statuses:     []models.PublishStatus{models.StatusPublished},
		Page:         store.Page{Limit: in.Limit, Offset: in.Offset},
	}
	if in.Kind != "" {
		kind := models.TopicKind(in.Kind)
		if !kind.Valid() {
			return query, huma.Error422UnprocessableEntity("unknown kind of topic")
		}
		query.Kinds = []models.TopicKind{kind}
	}
	if in.Level != "" {
		level := models.Level(in.Level)
		if !level.Valid() {
			return query, huma.Error422UnprocessableEntity("unknown level")
		}
		query.Levels = []models.Level{level}
	}

	box, err := optionalBounds(in.Bounds)
	if err != nil {
		return query, err
	}
	query.Bounds = box
	return query, nil
}

func (a *API) listTopics(ctx context.Context, in *TopicListInput) (*TopicsOutput, error) {
	query, err := topicQuery(in)
	if err != nil {
		return nil, err
	}

	key := cache.Keyed("topics.list", in.Collective, in.Kind, in.Level, in.Bounds,
		itoa(in.Limit), itoa(in.Offset))
	listing, err := cache.Fetch(a.cache, key, cache.Topics, func() (topicListing, error) {
		topics, total, err := a.store.ListTopics(ctx, query)
		if err != nil {
			return topicListing{}, err
		}
		ids := make([]string, 0, len(topics))
		for _, topic := range topics {
			ids = append(ids, topic.CollectiveID)
		}
		return topicListing{topics: topics, owners: a.ownersOf(ctx, ids), total: total}, nil
	})
	if err != nil {
		log.Error().Err(err).Msg("cannot list topics")
		return nil, huma.Error500InternalServerError("cannot list the topics")
	}

	listing = listing.visible()

	out := &TopicsOutput{}
	out.Body.Total = listing.total
	out.Body.Topics = make([]TopicItem, 0, len(listing.topics))
	for _, topic := range listing.topics {
		out.Body.Topics = append(out.Body.Topics, toTopicItem(topic, listing.owners[topic.CollectiveID]))
	}
	return out, nil
}

// TopicIDInput addresses a topic.
type TopicIDInput struct {
	ID string `path:"id"`
}

// TopicOutput is one topic.
type TopicOutput struct {
	Body TopicItem
}

func (a *API) getTopic(ctx context.Context, in *TopicIDInput) (*TopicOutput, error) {
	topic, err := a.store.Topic(ctx, in.ID)
	if errors.Is(err, store.ErrTopicNotFound) || (err == nil && !topic.Status.Public()) {
		return nil, huma.Error404NotFound("no such topic")
	}
	if err != nil {
		log.Error().Err(err).Str("topic", in.ID).Msg("cannot read a topic")
		return nil, huma.Error500InternalServerError("cannot read the topic")
	}

	owner := a.ownersOf(ctx, []string{topic.CollectiveID})[topic.CollectiveID]
	// A topic is public only while the collective behind it is. Otherwise an
	// alliance taken back to draft would leave its pages up with no author.
	if !owner.Status.Public() {
		return nil, huma.Error404NotFound("no such topic")
	}

	out := &TopicOutput{Body: toTopicItem(topic, owner)}
	out.Body.Followers, out.Body.Following = a.followState(ctx, models.FollowTopic, topic.ID)
	return out, nil
}

// --- the console ---

// topicFor loads a topic and checks the editor may manage its collective.
func (a *API) topicFor(ctx context.Context, id string) (*staff, models.Topic, models.Collective, error) {
	who, err := mustStaff(ctx)
	if err != nil {
		return nil, models.Topic{}, models.Collective{}, err
	}

	topic, err := a.store.Topic(ctx, id)
	if errors.Is(err, store.ErrTopicNotFound) {
		return nil, models.Topic{}, models.Collective{}, huma.Error404NotFound("no such topic")
	}
	if err != nil {
		log.Error().Err(err).Str("topic", id).Msg("cannot read a topic")
		return nil, models.Topic{}, models.Collective{}, huma.Error500InternalServerError("cannot read the topic")
	}

	_, collective, err := a.collectiveFor(ctx, topic.CollectiveID)
	if err != nil {
		return nil, models.Topic{}, models.Collective{}, huma.Error404NotFound("no such topic")
	}
	return who, topic, collective, nil
}

func (a *API) staffListTopics(ctx context.Context, in *CollectiveIDInput) (*TopicsOutput, error) {
	_, collective, err := a.collectiveFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}

	topics, total, err := a.store.ListTopics(ctx, store.TopicQuery{
		CollectiveID: collective.ID,
		Page:         store.Page{Limit: 1000},
	})
	if err != nil {
		log.Error().Err(err).Str("collective", collective.ID).Msg("cannot list topics for the console")
		return nil, huma.Error500InternalServerError("cannot list the topics")
	}

	out := &TopicsOutput{}
	out.Body.Total = total
	out.Body.Topics = make([]TopicItem, 0, len(topics))
	for _, topic := range topics {
		out.Body.Topics = append(out.Body.Topics, toTopicItem(topic, collective))
	}
	return out, nil
}

func (a *API) staffGetTopic(ctx context.Context, in *TopicIDInput) (*TopicOutput, error) {
	_, topic, collective, err := a.topicFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	out := &TopicOutput{Body: toTopicItem(topic, collective)}
	out.Body.Followers, _ = a.followState(ctx, models.FollowTopic, topic.ID)
	return out, nil
}

// TopicFields is what an editor's form sends for a topic.
type TopicFields struct {
	Kind    string `json:"kind" doc:"cut, reform, closure, privatisation or other"`
	Level   string `json:"level" doc:"federal, state or municipal"`
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
	Body    string `json:"body,omitempty"`

	Amount    int64  `json:"amount,omitempty" doc:"the money at stake in whole euros; zero for no figure"`
	SourceURL string `json:"source_url,omitempty" doc:"where the plan itself can be read"`
	Status    string `json:"status,omitempty" doc:"draft, published or archived"`

	PlaceInput
}

func (a *API) applyTopicFields(ctx context.Context, topic *models.Topic, fields TopicFields) error {
	title, err := cleanLine("the title", fields.Title, maxTitleRunes)
	if err != nil {
		return err
	}
	if title == "" {
		return huma.Error422UnprocessableEntity("a topic needs a title")
	}
	summary, err := cleanLine("the summary", fields.Summary, maxSummaryRunes)
	if err != nil {
		return err
	}
	body, err := cleanText("the text", fields.Body, maxBodyRunes)
	if err != nil {
		return err
	}
	source, err := cleanURL("the source", fields.SourceURL)
	if err != nil {
		return err
	}

	kind := models.TopicKind(strings.TrimSpace(fields.Kind))
	if !kind.Valid() {
		return huma.Error422UnprocessableEntity("say what kind of topic this is")
	}
	level := models.Level(strings.TrimSpace(fields.Level))
	if !level.Valid() {
		return huma.Error422UnprocessableEntity("say which level of government this is")
	}
	if fields.Amount < 0 {
		// The sign is in the kind: a cut of a hundred million is an amount of
		// a hundred million. A negative number here would be a minus sign the
		// page then puts a second minus sign in front of.
		return huma.Error422UnprocessableEntity("the amount is given without a minus sign")
	}

	status, err := nextStatus(fields.Status, topic.Status)
	if err != nil {
		return err
	}
	location, err := a.resolvePlace(ctx, fields.PlaceInput, topic.Location)
	if err != nil {
		return err
	}

	topic.Kind = kind
	topic.Level = level
	topic.Title = title
	topic.Summary = summary
	topic.Body = body
	topic.Amount = fields.Amount
	topic.SourceURL = source
	topic.Status = status
	topic.Location = location
	return nil
}

// nextStatus reads the status a form asked for, keeping the current one when
// it asked for none and starting from draft when there is none to keep.
func nextStatus(requested string, current models.PublishStatus) (models.PublishStatus, error) {
	status := models.PublishStatus(strings.TrimSpace(requested))
	if status == "" {
		status = current
	}
	if status == "" {
		status = models.StatusDraft
	}
	if !status.Valid() {
		return "", huma.Error422UnprocessableEntity("the status must be draft, published or archived")
	}
	return status, nil
}

// CreateTopicInput is a new topic for a collective.
type CreateTopicInput struct {
	ID   string `path:"id"`
	Body TopicFields
}

func (a *API) staffCreateTopic(ctx context.Context, in *CreateTopicInput) (*TopicOutput, error) {
	who, collective, err := a.collectiveFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}

	topic := &models.Topic{CollectiveID: collective.ID}
	if err := a.applyTopicFields(ctx, topic, in.Body); err != nil {
		return nil, err
	}

	published, err := a.store.SaveTopic(ctx, topic)
	if err != nil {
		log.Error().Err(err).Str("collective", collective.ID).Msg("cannot create a topic")
		return nil, huma.Error500InternalServerError("cannot create the topic")
	}

	a.audit(ctx, who, models.AuditCreate, "topic", topic.ID, collective.ID, topic.Title)
	if published {
		a.audit(ctx, who, models.AuditPublish, "topic", topic.ID, collective.ID, topic.Title)
		a.announceTopic(ctx, *topic, collective)
	}
	return &TopicOutput{Body: toTopicItem(*topic, collective)}, nil
}

// SaveTopicInput changes a topic.
type SaveTopicInput struct {
	ID   string `path:"id"`
	Body TopicFields
}

func (a *API) staffSaveTopic(ctx context.Context, in *SaveTopicInput) (*TopicOutput, error) {
	who, topic, collective, err := a.topicFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	before := topic.Status

	if err := a.applyTopicFields(ctx, &topic, in.Body); err != nil {
		return nil, err
	}

	published, err := a.store.SaveTopic(ctx, &topic)
	if err != nil {
		log.Error().Err(err).Str("topic", topic.ID).Msg("cannot save a topic")
		return nil, huma.Error500InternalServerError("cannot save the topic")
	}

	a.audit(ctx, who, statusAction(before, topic.Status), "topic", topic.ID, collective.ID, topic.Title)
	if published {
		a.announceTopic(ctx, topic, collective)
	}
	return &TopicOutput{Body: toTopicItem(topic, collective)}, nil
}

func (a *API) staffDeleteTopic(ctx context.Context, in *TopicIDInput) (*DoneOutput, error) {
	who, topic, collective, err := a.topicFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if err := a.store.DeleteTopic(ctx, topic.ID); err != nil && !errors.Is(err, store.ErrTopicNotFound) {
		log.Error().Err(err).Str("topic", topic.ID).Msg("cannot delete a topic")
		return nil, huma.Error500InternalServerError("cannot delete the topic")
	}

	a.audit(ctx, who, models.AuditDelete, "topic", topic.ID, collective.ID, topic.Title)
	return done(), nil
}

// announceTopic tells a collective's followers that it has taken up something
// new.
//
// Only when the collective itself is public: a topic published under a draft
// collective is not reachable by anybody, and a notification linking to a 404
// is worse than none.
func (a *API) announceTopic(ctx context.Context, topic models.Topic, collective models.Collective) {
	if collective.Status != models.StatusPublished {
		return
	}
	accounts, err := a.store.FollowersOf(ctx, collective.ID, "")
	if err != nil {
		log.Error().Err(err).Str("topic", topic.ID).Msg("cannot read who to tell about a topic")
		return
	}
	a.tell(ctx, accounts, Announcement{
		Kind:        models.NotifyTopicPublished,
		Title:       collective.Name,
		Body:        topic.Title,
		SubjectType: "topic",
		SubjectID:   topic.ID,
		Path:        "/topics/" + topic.ID,
	})
}
