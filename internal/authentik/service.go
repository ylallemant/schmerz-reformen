package authentik

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/rs/zerolog/log"
)

// The site's own service account: the identity the backend reads and changes
// the directory as, so it does not depend on the person who ran the setup
// wizard staying in the directory with the rights they had that day.
//
// # What it may do, and nothing more
//
// Authentik grants permissions to roles, and roles to groups. So the account
// gets a role of the site's own holding exactly what the backend does with
// the directory — read and create users and groups, put people in groups and
// take them out, mint a recovery link, read the flows and the brand to know
// whether anybody can be onboarded — and a group binding that role.
// Creating the provider, the application and the recovery flow stays with
// the person running the wizard: the service account cannot do that, and does
// not need to.

// servicePermissions are what the service account is granted, as
// app_label.codename.
//
// **Not in Authentik's published schema.** The codenames were taken from its
// models, which is why the wizard asks the instance for each one before
// anything is created, and tries the account's token afterwards: a name that
// is wrong for this version leaves the backend on the token it had, and says
// so, rather than switching it to a credential that cannot read a role.
var servicePermissions = []string{
	"authentik_core.view_user",
	"authentik_core.add_user",
	"authentik_core.change_user",
	"authentik_core.reset_user_password",
	"authentik_core.view_group",
	"authentik_core.add_group",
	"authentik_core.change_group",
	"authentik_core.add_user_to_group",
	"authentik_core.remove_user_from_group",
	"authentik_flows.view_flow",
	"authentik_brands.view_brand",
}

// ServiceAccount is what EnsureServiceAccount did.
type ServiceAccount struct {
	// Username is the account's, and Token the identifier of the API token
	// the backend keeps for it; Key is that token's secret.
	Username string
	Token    string
	Key      string

	// Ready says the account exists, holds its role, and its token was tried
	// and answered: the backend may switch to it.
	Ready bool

	// NotUsed says why the backend keeps the token it had instead, in words
	// for an operator. Empty when Ready.
	NotUsed string

	// Repaired lists what already existed and was brought back in line.
	Repaired []string
}

type role struct {
	PK   string `json:"pk"`
	Name string `json:"name"`
}

// EnsureServiceAccount gives the site an account of its own in the directory,
// reconciling whatever of it already exists.
//
// It never fails the wizard: a directory where it cannot be made — a
// permission this version does not know, a token that does not answer — is
// reported in NotUsed, and the backend carries on with the token it had.
func (c *Client) EnsureServiceAccount(ctx context.Context, appName string) (ServiceAccount, error) {
	username := appName + "-service"
	out := ServiceAccount{Username: username, Token: username + "-api"}

	// Every permission must exist on this instance before anything is made.
	var missing []string
	for _, permission := range servicePermissions {
		app, codename, _ := strings.Cut(permission, ".")
		var found page[struct {
			Codename string `json:"codename"`
			AppLabel string `json:"app_label"`
		}]
		path := "/rbac/permissions/?codename=" + url.QueryEscape(codename) +
			"&content_type__app_label=" + url.QueryEscape(app)
		if err := c.do(ctx, http.MethodGet, path, nil, &found); err != nil {
			return out, fmt.Errorf("look for the permission %s: %w", permission, err)
		}
		exists := false
		for _, result := range found.Results {
			exists = exists || (result.Codename == codename && result.AppLabel == app)
		}
		if !exists {
			missing = append(missing, permission)
		}
	}
	if len(missing) > 0 {
		out.NotUsed = "this Authentik does not know the permissions " + strings.Join(missing, ", ") +
			"; the backend keeps using the token of the person who ran the setup"
		log.Warn().Strs("missing", missing).Msg("authentik: no service account, permissions unknown to this version")
		return out, nil
	}

	roleName := appName + "-service"
	theRole, err := c.ensureRole(ctx, roleName)
	if err != nil {
		return out, err
	}
	// Assigned every run: granting a permission a role already holds changes
	// nothing, and a permission somebody took away is given back.
	if err := c.do(ctx, http.MethodPost,
		"/rbac/permissions/assigned_by_roles/"+url.PathEscape(theRole.PK)+"/assign/",
		map[string]any{"permissions": servicePermissions}, nil); err != nil {
		return out, fmt.Errorf("grant the service role its permissions: %w", err)
	}

	group, err := c.EnsureGroup(ctx, appName+"-service")
	if err != nil {
		return out, err
	}
	if !containsString(group.Roles, theRole.PK) {
		body := map[string]any{"roles": append(append([]string{}, group.Roles...), theRole.PK)}
		if err := c.do(ctx, http.MethodPatch, "/core/groups/"+url.PathEscape(group.PK)+"/", body, nil); err != nil {
			return out, fmt.Errorf("give the service group its role: %w", err)
		}
	}

	account, err := c.UserByUsername(ctx, username)
	switch {
	case errors.Is(err, ErrNotFound):
		var made struct {
			Username string `json:"username"`
			UserPK   int    `json:"user_pk"`
		}
		body := map[string]any{"name": username, "create_group": false, "expiring": false}
		if err := c.do(ctx, http.MethodPost, "/core/users/service_account/", body, &made); err != nil {
			return out, fmt.Errorf("create the service account: %w", err)
		}
		account = User{PK: made.UserPK, Username: made.Username, IsActive: true}
		log.Warn().Str("username", username).Msg("authentik: created this site's service account")
	case err != nil:
		return out, err
	case !account.IsActive:
		out.NotUsed = "the service account " + username + " is deactivated; reactivate it in Authentik"
		return out, nil
	}
	if !containsString(account.Groups, group.PK) {
		if err := c.AddToGroup(ctx, group.PK, account.PK); err != nil {
			return out, err
		}
	}

	// The account's own token, of intent "api": the app password the
	// service-account endpoint hands back is not accepted as a bearer token.
	token, err := c.ensureAPIToken(ctx, out.Token, account.PK,
		"The schmerz-reformen backend's credential: reads editors' groups and manages them. "+
			"Revoking it stops every sign-in to the console.")
	if err != nil {
		return out, err
	}
	out.Key = token.Key
	out.Repaired = append(out.Repaired, token.Repaired...)

	// Tried before it is trusted: what it will be asked on every request.
	trial, err := New(c.InstanceURL(), token.Key)
	if err == nil {
		_, err = trial.UserByUsername(ctx, username)
	}
	if err == nil {
		_, err = trial.GroupByName(ctx, group.Name)
	}
	if err != nil {
		out.NotUsed = "the service account's token was refused when tried (" + err.Error() +
			"); the backend keeps using the token of the person who ran the setup"
		log.Warn().Err(err).Msg("authentik: the service account's token does not work yet")
		return out, nil
	}
	out.Ready = true
	return out, nil
}

// ensureRole finds the site's role by name, or creates it.
func (c *Client) ensureRole(ctx context.Context, name string) (role, error) {
	found, ok, err := findItem(c, ctx, "/rbac/roles/", name, func(r role) string { return r.Name })
	if err != nil {
		return role{}, err
	}
	if ok {
		return found, nil
	}
	var made role
	if err := c.do(ctx, http.MethodPost, "/rbac/roles/", map[string]any{"name": name}, &made); err != nil {
		return role{}, fmt.Errorf("create the role %q: %w", name, err)
	}
	return made, nil
}
