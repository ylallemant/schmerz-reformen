package apiclient

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// Organisations are shared by every collective that lists them. The site's
// administrators create and delete them; each one's own administrators edit
// it and choose its people. Every call here is the console's.

// OrganisationValues is what an organisation says.
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
	ID   string `json:"id"`
	Slug string `json:"slug"`
	OrganisationValues

	// Its groups, and whether the editor asking administers it.
	AdminGroup  string `json:"admin_group,omitempty"`
	MemberGroup string `json:"member_group,omitempty"`
	Administers bool   `json:"administers"`

	// Collectives is filled when one organisation is read.
	Collectives []CollectiveRef `json:"collectives,omitempty"`
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

// Organisation returns one organisation, with where it is a member.
func (c *Client) Organisation(ctx context.Context, id string) (Organisation, error) {
	var organisation Organisation
	err := c.get(ctx, "/v1/staff/organisations/"+url.PathEscape(id), &organisation)
	return organisation, err
}

// CreateOrganisation creates one. The site's administrators only.
func (c *Client) CreateOrganisation(ctx context.Context, fields OrganisationFields) (Organisation, error) {
	var organisation Organisation
	err := c.write(ctx, http.MethodPost, "/v1/staff/organisations", fields, &organisation)
	return organisation, err
}

// SaveOrganisation changes one. Its administrators, and the site's.
func (c *Client) SaveOrganisation(ctx context.Context, id string, fields OrganisationFields) (Organisation, error) {
	var organisation Organisation
	err := c.write(ctx, http.MethodPut, "/v1/staff/organisations/"+url.PathEscape(id), fields, &organisation)
	return organisation, err
}

// DeleteOrganisation deletes one nothing uses. The site's administrators only.
func (c *Client) DeleteOrganisation(ctx context.Context, id string) error {
	return c.send(ctx, http.MethodDelete, "/v1/staff/organisations/"+url.PathEscape(id), "", nil)
}

// UploadOrganisationLogo replaces its logo.
func (c *Client) UploadOrganisationLogo(ctx context.Context, id, contentType string, data []byte) error {
	return c.send(ctx, http.MethodPut, "/v1/staff/organisations/"+url.PathEscape(id)+"/logo", contentType, data)
}

// DeleteOrganisationLogo removes it.
func (c *Client) DeleteOrganisationLogo(ctx context.Context, id string) error {
	return c.send(ctx, http.MethodDelete, "/v1/staff/organisations/"+url.PathEscape(id)+"/logo", "", nil)
}

// User is a console user, to choose from.
type User struct {
	PK       int    `json:"pk"`
	Username string `json:"username"`
	Name     string `json:"name,omitempty"`
	Email    string `json:"email,omitempty"`
}

// Users lists the console's users, for whoever gives roles.
func (c *Client) Users(ctx context.Context) ([]User, error) {
	var answer struct {
		Users []User `json:"users"`
	}
	err := c.get(ctx, "/v1/staff/users", &answer)
	return answer.Users, err
}

// EntryPeople is who holds the two roles on a collective or an organisation:
// its administrators, and its authors or members.
type EntryPeople struct {
	Admins []User `json:"admins"`
	Others []User `json:"others"`

	// MayGrantAdmins says the editor asking may change the administrators.
	MayGrantAdmins bool `json:"may_grant_admins"`
}

// CollectivePeople reads who administers a collective and who writes for it.
func (c *Client) CollectivePeople(ctx context.Context, id string) (EntryPeople, error) {
	var people EntryPeople
	err := c.get(ctx, "/v1/staff/collectives/"+url.PathEscape(id)+"/people", &people)
	return people, err
}

// OrganisationPeople reads who administers an organisation, and its people.
func (c *Client) OrganisationPeople(ctx context.Context, id string) (EntryPeople, error) {
	var people EntryPeople
	err := c.get(ctx, "/v1/staff/organisations/"+url.PathEscape(id)+"/people", &people)
	return people, err
}

// SetEntryRole gives or takes a role on an entry: kind is "collectives" or
// "organisations", role "admins", "authors" or "members".
func (c *Client) SetEntryRole(ctx context.Context, kind, id, role string, pk int, username string, give bool) error {
	method := http.MethodPut
	if !give {
		method = http.MethodDelete
	}
	path := "/v1/staff/" + kind + "/" + url.PathEscape(id) + "/people/" + url.PathEscape(role) + "/" +
		strconv.Itoa(pk) + "?username=" + url.QueryEscape(username)
	return c.send(ctx, method, path, "", nil)
}
