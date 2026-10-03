package apiclient

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Organisations are shared by every collective that lists them, and changed
// only by agreement: creating, changing and deleting one are proposals, which
// the backend carries out once enough other editors approve them. Every call
// here is the console's.

// OrganisationValues is what an organisation says — the organisation's own,
// or one side of a change.
type OrganisationValues struct {
	Name       string  `json:"name"`
	Kind       string  `json:"kind"`
	Website    string  `json:"website,omitempty"`
	ParentID   string  `json:"parent_id,omitempty"`
	ParentName string  `json:"parent_name,omitempty"`
	LogoID     string  `json:"logo_id,omitempty"`
	Place      string  `json:"place,omitempty"`
	Latitude   float64 `json:"latitude,omitempty"`
	Longitude  float64 `json:"longitude,omitempty"`
}

// Organisation is one organisation.
type Organisation struct {
	ID string `json:"id"`
	OrganisationValues

	// Collectives and Pending are filled when one organisation is read.
	Collectives []CollectiveRef `json:"collectives,omitempty"`
	Pending     []Change        `json:"pending,omitempty"`
}

// Vote is one editor's vote on a change.
type Vote struct {
	VoterName string    `json:"voter_name,omitempty"`
	Approve   bool      `json:"approve"`
	Comment   string    `json:"comment,omitempty"`
	At        time.Time `json:"at"`
}

// Change is a proposed creation, change or deletion of an organisation, as
// the signed-in editor sees it.
type Change struct {
	ID             string   `json:"id"`
	OrganisationID string   `json:"organisation_id"`
	Kind           string   `json:"kind"`
	Status         string   `json:"status"`
	Fields         []string `json:"fields,omitempty"`

	Before OrganisationValues `json:"before"`
	After  OrganisationValues `json:"after"`

	AuthorName string `json:"author_name,omitempty"`
	Mine       bool   `json:"mine"`
	MyVote     string `json:"my_vote,omitempty"`
	CanVote    bool   `json:"can_vote"`

	Votes      []Vote `json:"votes"`
	Approvals  int    `json:"approvals"`
	Rejections int    `json:"rejections"`
	Needed     int    `json:"needed"`

	Reason    string     `json:"reason,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	DecidedAt *time.Time `json:"decided_at,omitempty"`
}

// Name is what the change is about: the name it gives, or the one it had.
func (c Change) Name() string {
	if c.After.Name != "" {
		return c.After.Name
	}
	return c.Before.Name
}

// Sets reports whether the change sets a field.
func (c Change) Sets(field string) bool {
	for _, set := range c.Fields {
		if set == field {
			return true
		}
	}
	return false
}

// OrganisationFields is an organisation as an editor's form sends it.
type OrganisationFields struct {
	Name     string `json:"name"`
	Kind     string `json:"kind,omitempty"`
	Website  string `json:"website,omitempty"`
	ParentID string `json:"parent_id,omitempty"`

	Place
}

// Organisations returns every organisation, by name.
func (c *Client) Organisations(ctx context.Context) ([]Organisation, error) {
	var answer struct {
		Organisations []Organisation `json:"organisations"`
	}
	err := c.get(ctx, "/v1/staff/organisations", &answer)
	return answer.Organisations, err
}

// Organisation returns one organisation, with where it is a member and what
// is waiting to change it.
func (c *Client) Organisation(ctx context.Context, id string) (Organisation, error) {
	var organisation Organisation
	err := c.get(ctx, "/v1/staff/organisations/"+url.PathEscape(id), &organisation)
	return organisation, err
}

// ProposeOrganisation proposes a new organisation.
func (c *Client) ProposeOrganisation(ctx context.Context, fields OrganisationFields) (Change, error) {
	var change Change
	err := c.write(ctx, http.MethodPost, "/v1/staff/organisations", fields, &change)
	return change, err
}

// ProposeOrganisationUpdate proposes a change to an organisation.
func (c *Client) ProposeOrganisationUpdate(ctx context.Context, id string, fields OrganisationFields) (Change, error) {
	var change Change
	err := c.write(ctx, http.MethodPut, "/v1/staff/organisations/"+url.PathEscape(id), fields, &change)
	return change, err
}

// ProposeOrganisationDeletion proposes deleting an organisation.
func (c *Client) ProposeOrganisationDeletion(ctx context.Context, id string) (Change, error) {
	var change Change
	err := c.write(ctx, http.MethodPost, "/v1/staff/organisations/"+url.PathEscape(id)+"/deletion", struct{}{}, &change)
	return change, err
}

// ProposeOrganisationLogo proposes a new logo.
func (c *Client) ProposeOrganisationLogo(ctx context.Context, id, contentType string, data []byte) error {
	return c.send(ctx, http.MethodPut,
		"/v1/staff/organisations/"+url.PathEscape(id)+"/logo", contentType, data)
}

// ProposeOrganisationLogoRemoval proposes removing the logo.
func (c *Client) ProposeOrganisationLogoRemoval(ctx context.Context, id string) error {
	return c.send(ctx, http.MethodDelete, "/v1/staff/organisations/"+url.PathEscape(id)+"/logo", "", nil)
}

// Changes lists changes, newest first: status is "pending", "decided" or
// "all", and organisationID narrows it to one organisation when set.
func (c *Client) Changes(ctx context.Context, status, organisationID string, limit int) ([]Change, int64, error) {
	query := url.Values{}
	if status != "" {
		query.Set("status", status)
	}
	if organisationID != "" {
		query.Set("organisation", organisationID)
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	var answer struct {
		Changes []Change `json:"changes"`
		Total   int64    `json:"total"`
	}
	err := c.get(ctx, "/v1/staff/changes?"+query.Encode(), &answer)
	return answer.Changes, answer.Total, err
}

// Change returns one change with its votes.
func (c *Client) Change(ctx context.Context, id string) (Change, error) {
	var change Change
	err := c.get(ctx, "/v1/staff/changes/"+url.PathEscape(id), &change)
	return change, err
}

// VoteOnChange approves or rejects a change.
func (c *Client) VoteOnChange(ctx context.Context, id string, approve bool, comment string) (Change, error) {
	var change Change
	err := c.write(ctx, http.MethodPost, "/v1/staff/changes/"+url.PathEscape(id)+"/votes",
		map[string]any{"approve": approve, "comment": comment}, &change)
	return change, err
}

// WithdrawChange takes back a change the editor proposed.
func (c *Client) WithdrawChange(ctx context.Context, id string) (Change, error) {
	var change Change
	err := c.write(ctx, http.MethodPost, "/v1/staff/changes/"+url.PathEscape(id)+"/withdrawal", struct{}{}, &change)
	return change, err
}
