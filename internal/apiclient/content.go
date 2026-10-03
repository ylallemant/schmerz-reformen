package apiclient

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// The types in this file are the backend's answers as the web services read
// them. They are declared here rather than imported from the backend on
// purpose: the two exposed services must not link the database driver, the
// storage drivers and the rest of what the backend is made of, and importing
// its types would pull all of it in.

// CollectiveRef is a collective as something else names it.
type CollectiveRef struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Slug   string `json:"slug"`
	LogoID string `json:"logo_id,omitempty"`
}

// Member is one organisation in a collective.
type Member struct {
	ID             string `json:"id"`
	OrganisationID string `json:"organisation_id"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	Website        string `json:"website,omitempty"`
	LogoID         string `json:"logo_id,omitempty"`
	Place          string `json:"place,omitempty"`
	Position       int    `json:"position"`
}

// Collective is an alliance of organisations.
type Collective struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Summary     string `json:"summary,omitempty"`
	Description string `json:"description,omitempty"`
	Website     string `json:"website,omitempty"`
	Contact     string `json:"contact,omitempty"`
	LogoID      string `json:"logo_id,omitempty"`
	Status      string `json:"status"`

	Place     string  `json:"place,omitempty"`
	Latitude  float64 `json:"latitude,omitempty"`
	Longitude float64 `json:"longitude,omitempty"`

	Members []Member `json:"members,omitempty"`

	Followers int64 `json:"followers"`
	Following bool  `json:"following"`

	// AuthGroup is filled on the console's routes only.
	AuthGroup string `json:"auth_group,omitempty"`
}

// TopicRef is a topic as something else names it.
type TopicRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
	Place string `json:"place,omitempty"`
}

// Topic is a cut, a reform or a closure a collective is campaigning on.
type Topic struct {
	ID           string         `json:"id"`
	CollectiveID string         `json:"collective_id"`
	Collective   *CollectiveRef `json:"collective,omitempty"`

	Kind  string `json:"kind"`
	Level string `json:"level"`

	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
	Body    string `json:"body,omitempty"`

	Amount    int64  `json:"amount,omitempty"`
	SourceURL string `json:"source_url,omitempty"`

	Place     string  `json:"place,omitempty"`
	Latitude  float64 `json:"latitude,omitempty"`
	Longitude float64 `json:"longitude,omitempty"`

	Status      string     `json:"status"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`

	Followers int64 `json:"followers"`
	Following bool  `json:"following"`
}

// Update is one piece of news about a topic.
type Update struct {
	ID string `json:"id"`

	TopicID string    `json:"topic_id"`
	Topic   *TopicRef `json:"topic,omitempty"`

	CollectiveID string         `json:"collective_id"`
	Collective   *CollectiveRef `json:"collective,omitempty"`

	Title     string `json:"title"`
	Body      string `json:"body"`
	Excerpt   string `json:"excerpt"`
	Truncated bool   `json:"truncated"`
	SourceURL string `json:"source_url,omitempty"`

	Status      string     `json:"status"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// Action is something happening at a time and a place.
type Action struct {
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

	Status      string     `json:"status"`
	Cancelled   bool       `json:"cancelled"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`

	Participants  int64 `json:"participants"`
	Participating bool  `json:"participating"`
}

// pageQuery renders a limit and an offset, leaving out whichever is unset so
// the backend's own defaults apply.
func pageQuery(values url.Values, limit, offset int) {
	if limit > 0 {
		values.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		values.Set("offset", strconv.Itoa(offset))
	}
}

// withQuery appends a query string when there is one.
func withQuery(path string, values url.Values) string {
	if len(values) == 0 {
		return path
	}
	return path + "?" + values.Encode()
}

// ListCollectives returns the published collectives, with how many there are.
func (c *Client) ListCollectives(ctx context.Context) ([]Collective, int64, error) {
	var payload struct {
		Collectives []Collective `json:"collectives"`
		Total       int64        `json:"total"`
	}
	if err := c.get(ctx, "/v1/collectives", &payload); err != nil {
		return nil, 0, err
	}
	return payload.Collectives, payload.Total, nil
}

// GetCollective returns one collective by its public address.
func (c *Client) GetCollective(ctx context.Context, slug string) (Collective, error) {
	var collective Collective
	err := c.get(ctx, "/v1/collectives/"+url.PathEscape(slug), &collective)
	return collective, err
}

// TopicFilter narrows a listing of topics.
type TopicFilter struct {
	Collective string
	Kind       string
	Level      string
	Limit      int
	Offset     int
}

// ListTopics returns published topics, most recently published first.
func (c *Client) ListTopics(ctx context.Context, filter TopicFilter) ([]Topic, int64, error) {
	values := url.Values{}
	if filter.Collective != "" {
		values.Set("collective", filter.Collective)
	}
	if filter.Kind != "" {
		values.Set("kind", filter.Kind)
	}
	if filter.Level != "" {
		values.Set("level", filter.Level)
	}
	pageQuery(values, filter.Limit, filter.Offset)

	var payload struct {
		Topics []Topic `json:"topics"`
		Total  int64   `json:"total"`
	}
	if err := c.get(ctx, withQuery("/v1/topics", values), &payload); err != nil {
		return nil, 0, err
	}
	return payload.Topics, payload.Total, nil
}

// GetTopic returns one topic.
func (c *Client) GetTopic(ctx context.Context, id string) (Topic, error) {
	var topic Topic
	err := c.get(ctx, "/v1/topics/"+url.PathEscape(id), &topic)
	return topic, err
}

// UpdateFilter narrows the feed.
type UpdateFilter struct {
	Topic      string
	Collective string

	// Following asks for the signed-in reader's own feed. It needs a client
	// made with As.
	Following bool

	Limit  int
	Offset int
}

// ListUpdates returns a page of the feed, newest first.
func (c *Client) ListUpdates(ctx context.Context, filter UpdateFilter) ([]Update, int64, error) {
	values := url.Values{}
	if filter.Topic != "" {
		values.Set("topic", filter.Topic)
	}
	if filter.Collective != "" {
		values.Set("collective", filter.Collective)
	}
	if filter.Following {
		values.Set("following", "true")
	}
	pageQuery(values, filter.Limit, filter.Offset)

	var payload struct {
		Updates []Update `json:"updates"`
		Total   int64    `json:"total"`
	}
	if err := c.get(ctx, withQuery("/v1/updates", values), &payload); err != nil {
		return nil, 0, err
	}
	return payload.Updates, payload.Total, nil
}

// GetUpdate returns one update.
func (c *Client) GetUpdate(ctx context.Context, id string) (Update, error) {
	var update Update
	err := c.get(ctx, "/v1/updates/"+url.PathEscape(id), &update)
	return update, err
}

// ActionFilter narrows the calendar.
type ActionFilter struct {
	Collective string
	Topic      string
	Kind       string

	// From and To bound the start time. With both zero the backend starts the
	// listing now.
	From time.Time
	To   time.Time

	// Mine asks for the actions the signed-in reader is coming to.
	Mine bool

	Limit  int
	Offset int
}

// ListActions returns published actions in the order they happen.
func (c *Client) ListActions(ctx context.Context, filter ActionFilter) ([]Action, int64, error) {
	values := url.Values{}
	if filter.Collective != "" {
		values.Set("collective", filter.Collective)
	}
	if filter.Topic != "" {
		values.Set("topic", filter.Topic)
	}
	if filter.Kind != "" {
		values.Set("kind", filter.Kind)
	}
	if !filter.From.IsZero() {
		values.Set("from", filter.From.UTC().Format(time.RFC3339))
	}
	if !filter.To.IsZero() {
		values.Set("to", filter.To.UTC().Format(time.RFC3339))
	}
	if filter.Mine {
		values.Set("mine", "true")
	}
	pageQuery(values, filter.Limit, filter.Offset)

	var payload struct {
		Actions []Action `json:"actions"`
		Total   int64    `json:"total"`
	}
	if err := c.get(ctx, withQuery("/v1/actions", values), &payload); err != nil {
		return nil, 0, err
	}
	return payload.Actions, payload.Total, nil
}

// GetAction returns one action.
func (c *Client) GetAction(ctx context.Context, id string) (Action, error) {
	var action Action
	err := c.get(ctx, "/v1/actions/"+url.PathEscape(id), &action)
	return action, err
}

// MapTotals is how much there is, everywhere.
type MapTotals struct {
	Topics      int64 `json:"topics"`
	Actions     int64 `json:"actions"`
	Collectives int64 `json:"collectives"`
	Amount      int64 `json:"amount"`
}

// MapView is what one viewport shows.
type MapView struct {
	Topics      []Topic      `json:"topics"`
	Actions     []Action     `json:"actions"`
	Collectives []Collective `json:"collectives"`
	Totals      MapTotals    `json:"totals"`
}

// ReadMap returns everything pinned inside a viewport, given as
// "north,south,east,west". An empty viewport is everywhere.
func (c *Client) ReadMap(ctx context.Context, bounds string) (MapView, error) {
	values := url.Values{}
	if bounds != "" {
		values.Set("bounds", bounds)
	}

	var view MapView
	err := c.get(ctx, withQuery("/v1/map", values), &view)
	return view, err
}

// Media returns an uploaded image.
func (c *Client) Media(ctx context.Context, id string) (Asset, error) {
	return c.getAsset(ctx, "/v1/media/"+url.PathEscape(id))
}

// --- what a signed-in reader does ---

// Follow starts following a collective or a topic.
func (c *Client) Follow(ctx context.Context, target, id string) error {
	return c.send(ctx, http.MethodPut,
		"/v1/follows/"+url.PathEscape(target)+"/"+url.PathEscape(id), "", nil)
}

// Unfollow stops following it.
func (c *Client) Unfollow(ctx context.Context, target, id string) error {
	return c.send(ctx, http.MethodDelete,
		"/v1/follows/"+url.PathEscape(target)+"/"+url.PathEscape(id), "", nil)
}

// Following is everything one account follows.
type Following struct {
	Collectives []Collective `json:"collectives"`
	Topics      []Topic      `json:"topics"`
}

// ListFollowing returns what the signed-in reader follows.
func (c *Client) ListFollowing(ctx context.Context) (Following, error) {
	var following Following
	err := c.get(ctx, "/v1/accounts/me/following", &following)
	return following, err
}

// Participate records that the reader intends to come to an action.
func (c *Client) Participate(ctx context.Context, actionID string) error {
	return c.send(ctx, http.MethodPut,
		"/v1/actions/"+url.PathEscape(actionID)+"/participation", "", nil)
}

// Withdraw removes that intent.
func (c *Client) Withdraw(ctx context.Context, actionID string) error {
	return c.send(ctx, http.MethodDelete,
		"/v1/actions/"+url.PathEscape(actionID)+"/participation", "", nil)
}
