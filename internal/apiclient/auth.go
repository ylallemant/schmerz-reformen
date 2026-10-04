package apiclient

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// How the console signs editors in, and the people who may. Everything that
// touches the directory happens in the backend; the console holds no
// credential to it.

// AuthSettings is the sign-in configuration, without any secret.
type AuthSettings struct {
	Provisioned    bool       `json:"provisioned"`
	AppName        string     `json:"app_name,omitempty"`
	InstanceURL    string     `json:"instance_url,omitempty"`
	ConsoleURL     string     `json:"console_url,omitempty"`
	ClientID       string     `json:"client_id,omitempty"`
	Issuer         string     `json:"issuer,omitempty"`
	AdminGroupName string     `json:"admin_group_name,omitempty"`
	ProvisionedBy  string     `json:"provisioned_by,omitempty"`
	ProvisionedAt  *time.Time `json:"provisioned_at,omitempty"`
}

// AuthSettings reads it. Needs a client made with AsConsole.
func (c *Client) AuthSettings(ctx context.Context) (AuthSettings, error) {
	var settings AuthSettings
	err := c.get(ctx, "/v1/console/auth", &settings)
	return settings, err
}

// ClientSecret reads the one secret the console holds: what it exchanges an
// authorisation code with.
func (c *Client) ClientSecret(ctx context.Context) (string, error) {
	var answer struct {
		ClientSecret string `json:"client_secret"`
	}
	err := c.get(ctx, "/v1/console/auth/credentials", &answer)
	return answer.ClientSecret, err
}

// ProvisionRequest is what the setup wizard was given.
type ProvisionRequest struct {
	InstanceURL   string `json:"instance_url"`
	Token         string `json:"token"`
	AppName       string `json:"app_name,omitempty"`
	ConsoleURL    string `json:"console_url"`
	AdminUsername string `json:"admin_username,omitempty"`
	AdminName     string `json:"admin_name,omitempty"`
	Reprovision   bool   `json:"reprovision,omitempty"`
}

// Provisioning is what the backend did with it.
type Provisioning struct {
	Done          bool     `json:"done"`
	Missing       []string `json:"missing,omitempty"`
	AdminUsername string   `json:"admin_username,omitempty"`
	TokenOwner    string   `json:"token_owner,omitempty"`
	InstanceURL   string   `json:"instance_url,omitempty"`
	AppName       string   `json:"app_name,omitempty"`
	RecoveryReady bool     `json:"recovery_ready"`
	OwnToken      string   `json:"own_token,omitempty"`
	AdminLink     string   `json:"admin_link,omitempty"`

	// Reconciled is what already existed in the directory and was brought
	// back in line.
	Reconciled []string `json:"reconciled,omitempty"`
}

// provisionTimeout is how long provisioning may take: twenty-odd round trips
// to somebody else's Authentik, forty seconds measured against a remote one.
const provisionTimeout = 3 * time.Minute

// Provision runs the setup wizard's request. Needs a client made with
// AsConsole.
func (c *Client) Provision(ctx context.Context, request ProvisionRequest) (Provisioning, error) {
	var result Provisioning
	err := c.WithTimeout(provisionTimeout).write(ctx, http.MethodPost, "/v1/console/auth/provision", request, &result)
	return result, err
}

// Person is somebody with a role in the console.
type Person struct {
	PK          int      `json:"pk"`
	Username    string   `json:"username"`
	Name        string   `json:"name,omitempty"`
	Admin       bool     `json:"admin"`
	Collectives []string `json:"collectives"`
	Self        bool     `json:"self"`
}

// Edits reports whether they edit for a collective, for the page's checkboxes.
func (p Person) Edits(collectiveID string) bool {
	for _, id := range p.Collectives {
		if id == collectiveID {
			return true
		}
	}
	return false
}

// PeopleCollective is a collective somebody can be made an editor of.
type PeopleCollective struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Group string `json:"group"`
}

// People is the staff list.
type People struct {
	People        []Person           `json:"people"`
	Collectives   []PeopleCollective `json:"collectives"`
	AdminGroup    string             `json:"admin_group"`
	RecoveryReady bool               `json:"recovery_ready"`
}

// People lists who may use the console. Administrators only.
func (c *Client) People(ctx context.Context) (People, error) {
	var people People
	err := c.get(ctx, "/v1/staff/people", &people)
	return people, err
}

// Roles is what somebody may do.
type Roles struct {
	Admin       bool     `json:"admin"`
	Collectives []string `json:"collectives,omitempty"`
}

// WayIn is a single-use link, shown once.
type WayIn struct {
	Username string `json:"username"`
	Link     string `json:"link,omitempty"`

	// Adopted says the account existed already and was given its roles as it
	// is, so it signs in with what it has and no link was minted.
	Adopted bool `json:"adopted"`
}

// InvitePerson creates somebody with their roles and returns their way in.
func (c *Client) InvitePerson(ctx context.Context, username, name string, roles Roles) (WayIn, error) {
	var link WayIn
	err := c.write(ctx, http.MethodPost, "/v1/staff/people", map[string]any{
		"username": username, "name": name, "admin": roles.Admin, "collectives": roles.Collectives,
	}, &link)
	return link, err
}

// SetPersonRoles changes what somebody may do.
func (c *Client) SetPersonRoles(ctx context.Context, pk int, username string, roles Roles) error {
	return c.write(ctx, http.MethodPut, "/v1/staff/people/"+strconv.Itoa(pk), map[string]any{
		"username": username, "admin": roles.Admin, "collectives": roles.Collectives,
	}, nil)
}

// RelinkPerson mints a fresh way in.
func (c *Client) RelinkPerson(ctx context.Context, pk int, username string) (WayIn, error) {
	var link WayIn
	err := c.write(ctx, http.MethodPost, "/v1/staff/people/"+strconv.Itoa(pk)+"/link",
		map[string]any{"username": username}, &link)
	return link, err
}

// RemovePerson takes somebody out of every one of this site's groups.
func (c *Client) RemovePerson(ctx context.Context, pk int, username string) error {
	return c.send(ctx, http.MethodDelete,
		"/v1/staff/people/"+strconv.Itoa(pk)+"?username="+url.QueryEscape(username), "", nil)
}
