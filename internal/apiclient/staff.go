package apiclient

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Everything in this file is the console's. Each call needs a client made
// with AsStaff: the backend refuses these routes to anybody else, and decides
// for itself what the identity it is given may do.

// Place is where something is, as a form sends it.
type Place struct {
	Latitude  float64 `json:"latitude,omitempty"`
	Longitude float64 `json:"longitude,omitempty"`

	// Zoom is the map's zoom when the pin was dropped: how precise the editor
	// meant to be.
	Zoom int `json:"zoom,omitempty"`

	// Place is the name to show. Empty has the backend name the pin itself.
	Place string `json:"place,omitempty"`
}

// StaffProfile is the backend's own reading of who is signed in to the
// console.
type StaffProfile struct {
	Subject string `json:"subject"`
	Name    string `json:"name,omitempty"`
	Admin   bool   `json:"admin"`

	// Groups are the groups the backend decided by — the directory's, once
	// one is provisioned. The console keeps them to draw its menus.
	Groups []string `json:"groups"`

	Collectives []Collective `json:"collectives"`

	// Waiting is how many changes to organisations wait for this editor's
	// vote.
	Waiting int `json:"waiting"`
}

// StaffMe returns what the signed-in editor may do.
func (c *Client) StaffMe(ctx context.Context) (StaffProfile, error) {
	var profile StaffProfile
	err := c.get(ctx, "/v1/staff/me", &profile)
	return profile, err
}

// StaffCollective returns one collective as its editors see it.
func (c *Client) StaffCollective(ctx context.Context, id string) (Collective, error) {
	var collective Collective
	err := c.get(ctx, "/v1/staff/collectives/"+url.PathEscape(id), &collective)
	return collective, err
}

// CollectiveFields is a collective as an editor's form sends it.
type CollectiveFields struct {
	Name        string `json:"name"`
	Slug        string `json:"slug,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Description string `json:"description,omitempty"`
	Website     string `json:"website,omitempty"`
	Contact     string `json:"contact,omitempty"`
	AuthGroup   string `json:"auth_group,omitempty"`
	Status      string `json:"status,omitempty"`

	Place
}

// CreateCollective founds a collective. Administrators only.
func (c *Client) CreateCollective(ctx context.Context, fields CollectiveFields) (Collective, error) {
	var collective Collective
	err := c.write(ctx, http.MethodPost, "/v1/staff/collectives", fields, &collective)
	return collective, err
}

// SaveCollective changes a collective's profile.
func (c *Client) SaveCollective(ctx context.Context, id string, fields CollectiveFields) (Collective, error) {
	var collective Collective
	err := c.write(ctx, http.MethodPut, "/v1/staff/collectives/"+url.PathEscape(id), fields, &collective)
	return collective, err
}

// DeleteCollective removes a collective and everything it published.
func (c *Client) DeleteCollective(ctx context.Context, id string) error {
	return c.send(ctx, http.MethodDelete, "/v1/staff/collectives/"+url.PathEscape(id), "", nil)
}

// MemberFields adds an organisation to a collective, or moves it in the list.
type MemberFields struct {
	OrganisationID string `json:"organisation_id,omitempty"`
	Position       int    `json:"position,omitempty"`
}

// CreateMember adds an existing organisation to a collective.
func (c *Client) CreateMember(ctx context.Context, collectiveID string, fields MemberFields) (Member, error) {
	var member Member
	err := c.write(ctx, http.MethodPost,
		"/v1/staff/collectives/"+url.PathEscape(collectiveID)+"/members", fields, &member)
	return member, err
}

// SaveMember moves an organisation in a collective's list.
func (c *Client) SaveMember(ctx context.Context, id string, fields MemberFields) (Member, error) {
	var member Member
	err := c.write(ctx, http.MethodPut, "/v1/staff/members/"+url.PathEscape(id), fields, &member)
	return member, err
}

// DeleteMember takes an organisation out of a collective.
func (c *Client) DeleteMember(ctx context.Context, id string) error {
	return c.send(ctx, http.MethodDelete, "/v1/staff/members/"+url.PathEscape(id), "", nil)
}

// UploadCollectiveLogo replaces a collective's logo.
func (c *Client) UploadCollectiveLogo(ctx context.Context, id, contentType string, data []byte) error {
	return c.send(ctx, http.MethodPut,
		"/v1/staff/collectives/"+url.PathEscape(id)+"/logo", contentType, data)
}

// DeleteCollectiveLogo removes it.
func (c *Client) DeleteCollectiveLogo(ctx context.Context, id string) error {
	return c.send(ctx, http.MethodDelete, "/v1/staff/collectives/"+url.PathEscape(id)+"/logo", "", nil)
}

// StaffTopics returns a collective's topics at every status.
func (c *Client) StaffTopics(ctx context.Context, collectiveID string) ([]Topic, error) {
	var payload struct {
		Topics []Topic `json:"topics"`
	}
	err := c.get(ctx, "/v1/staff/collectives/"+url.PathEscape(collectiveID)+"/topics", &payload)
	return payload.Topics, err
}

// StaffTopic returns one topic as its editors see it.
func (c *Client) StaffTopic(ctx context.Context, id string) (Topic, error) {
	var topic Topic
	err := c.get(ctx, "/v1/staff/topics/"+url.PathEscape(id), &topic)
	return topic, err
}

// TopicFields is a topic as an editor's form sends it.
type TopicFields struct {
	Kind      string `json:"kind"`
	Level     string `json:"level"`
	Title     string `json:"title"`
	Summary   string `json:"summary,omitempty"`
	Body      string `json:"body,omitempty"`
	Amount    int64  `json:"amount,omitempty"`
	SourceURL string `json:"source_url,omitempty"`
	Status    string `json:"status,omitempty"`

	Place
}

// CreateTopic creates a topic under a collective.
func (c *Client) CreateTopic(ctx context.Context, collectiveID string, fields TopicFields) (Topic, error) {
	var topic Topic
	err := c.write(ctx, http.MethodPost,
		"/v1/staff/collectives/"+url.PathEscape(collectiveID)+"/topics", fields, &topic)
	return topic, err
}

// SaveTopic changes a topic.
func (c *Client) SaveTopic(ctx context.Context, id string, fields TopicFields) (Topic, error) {
	var topic Topic
	err := c.write(ctx, http.MethodPut, "/v1/staff/topics/"+url.PathEscape(id), fields, &topic)
	return topic, err
}

// DeleteTopic removes a topic and its updates.
func (c *Client) DeleteTopic(ctx context.Context, id string) error {
	return c.send(ctx, http.MethodDelete, "/v1/staff/topics/"+url.PathEscape(id), "", nil)
}

// StaffUpdates returns a topic's updates at every status.
func (c *Client) StaffUpdates(ctx context.Context, topicID string) ([]Update, error) {
	var payload struct {
		Updates []Update `json:"updates"`
	}
	err := c.get(ctx, "/v1/staff/topics/"+url.PathEscape(topicID)+"/updates", &payload)
	return payload.Updates, err
}

// StaffUpdate returns one update as its editors see it.
func (c *Client) StaffUpdate(ctx context.Context, id string) (Update, error) {
	var update Update
	err := c.get(ctx, "/v1/staff/updates/"+url.PathEscape(id), &update)
	return update, err
}

// UpdateFields is an update as an editor's form sends it.
type UpdateFields struct {
	Title     string `json:"title"`
	Body      string `json:"body"`
	SourceURL string `json:"source_url,omitempty"`
	Status    string `json:"status,omitempty"`
}

// CreateUpdate writes an update on a topic.
func (c *Client) CreateUpdate(ctx context.Context, topicID string, fields UpdateFields) (Update, error) {
	var update Update
	err := c.write(ctx, http.MethodPost,
		"/v1/staff/topics/"+url.PathEscape(topicID)+"/updates", fields, &update)
	return update, err
}

// SaveUpdate changes an update.
func (c *Client) SaveUpdate(ctx context.Context, id string, fields UpdateFields) (Update, error) {
	var update Update
	err := c.write(ctx, http.MethodPut, "/v1/staff/updates/"+url.PathEscape(id), fields, &update)
	return update, err
}

// DeleteUpdate removes an update.
func (c *Client) DeleteUpdate(ctx context.Context, id string) error {
	return c.send(ctx, http.MethodDelete, "/v1/staff/updates/"+url.PathEscape(id), "", nil)
}

// StaffActions returns a collective's actions at every status, latest first.
func (c *Client) StaffActions(ctx context.Context, collectiveID string) ([]Action, error) {
	var payload struct {
		Actions []Action `json:"actions"`
	}
	err := c.get(ctx, "/v1/staff/collectives/"+url.PathEscape(collectiveID)+"/actions", &payload)
	return payload.Actions, err
}

// StaffAction returns one action as its editors see it.
func (c *Client) StaffAction(ctx context.Context, id string) (Action, error) {
	var action Action
	err := c.get(ctx, "/v1/staff/actions/"+url.PathEscape(id), &action)
	return action, err
}

// ActionFields is an action as an editor's form sends it.
//
// The times are RFC 3339 strings rather than time.Time: an absent end is the
// empty string, which a zero time would not encode as.
type ActionFields struct {
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	TopicID     string `json:"topic_id,omitempty"`
	StartsAt    string `json:"starts_at"`
	EndsAt      string `json:"ends_at,omitempty"`
	ExternalURL string `json:"external_url,omitempty"`
	Status      string `json:"status,omitempty"`
	Cancelled   bool   `json:"cancelled,omitempty"`

	Place
}

// CreateAction announces an action under a collective.
func (c *Client) CreateAction(ctx context.Context, collectiveID string, fields ActionFields) (Action, error) {
	var action Action
	err := c.write(ctx, http.MethodPost,
		"/v1/staff/collectives/"+url.PathEscape(collectiveID)+"/actions", fields, &action)
	return action, err
}

// SaveAction changes an action.
func (c *Client) SaveAction(ctx context.Context, id string, fields ActionFields) (Action, error) {
	var action Action
	err := c.write(ctx, http.MethodPut, "/v1/staff/actions/"+url.PathEscape(id), fields, &action)
	return action, err
}

// DeleteAction removes an action.
func (c *Client) DeleteAction(ctx context.Context, id string) error {
	return c.send(ctx, http.MethodDelete, "/v1/staff/actions/"+url.PathEscape(id), "", nil)
}

// AuditEntry is one write made through the console.
type AuditEntry struct {
	ID           string    `json:"id"`
	At           time.Time `json:"at"`
	Actor        string    `json:"actor"`
	ActorName    string    `json:"actor_name,omitempty"`
	Action       string    `json:"action"`
	SubjectType  string    `json:"subject_type"`
	SubjectID    string    `json:"subject_id"`
	CollectiveID string    `json:"collective_id,omitempty"`
	Collective   string    `json:"collective,omitempty"`
	Summary      string    `json:"summary,omitempty"`
}

// Audit returns a page of the audit log, newest first, with how many entries
// there are.
func (c *Client) Audit(ctx context.Context, limit, offset int) ([]AuditEntry, int64, error) {
	values := url.Values{}
	if limit > 0 {
		values.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		values.Set("offset", strconv.Itoa(offset))
	}

	var payload struct {
		Entries []AuditEntry `json:"entries"`
		Total   int64        `json:"total"`
	}
	err := c.get(ctx, withQuery("/v1/staff/audit", values), &payload)
	return payload.Entries, payload.Total, err
}
