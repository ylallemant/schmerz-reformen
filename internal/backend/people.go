package backend

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/authentik"
	"github.com/ylallemant/schmerz-reformen/internal/store"
)

// The people who may use the console, as the directory knows them.
//
// # The roles are groups
//
//	admin               <app>-admins: everything, everywhere
//	user                <app>-users: may sign in and read; can be given roles
//	collective_admin    a collective's AdminGroup: its profile, members, authors
//	collective_author   a collective's AuthorGroup: its topics, news, actions
//	organisation_admin  an organisation's AdminGroup: it, and its people
//	organisation member an organisation's MemberGroup
//
// Who gives which: the site's administrators add people and make
// administrators — of the site, and of each collective; a collective's
// administrators choose its authors; an organisation's administrators choose
// its members and its other administrators. See entrypeople.go.
//
// # It manages roles, never people
//
// This site provisioned itself into somebody else's directory. It creates
// accounts when asked and puts them in its own groups, and that is the whole
// of what it does to the directory: removing somebody takes them out of this
// site's groups and leaves their account exactly as it was.
//
// # No password, and an email only to be reached at
//
// A new person is handed a single-use recovery link an administrator passes
// on themselves, ending with their own passkey. An existing account is adopted
// as it is, and needs nothing handed over.
//
// Their email address is information: how the movement's organisers reach
// them outside the console. It is kept on their account in the directory,
// shown to the site's administrators and to whoever runs a collective or an
// organisation with them, and **nothing here sends to it or signs in with
// it**. It is optional throughout.
func (a *API) registerPeopleRoutes(api huma.API) {
	huma.Register(api, adminOnly(huma.Operation{
		OperationID: "staff-list-people",
		Method:      http.MethodGet,
		Path:        "/v1/staff/people",
		Summary:     "List who may use the console, with every role they hold",
		Tags:        []string{"People"},
	}), a.staffListPeople)

	huma.Register(api, adminOnly(huma.Operation{
		OperationID: "staff-invite-person",
		Method:      http.MethodPost,
		Path:        "/v1/staff/people",
		Summary:     "Add somebody to the console",
		Description: "Creates the account in the users' group — and the administrators' when " +
			"asked — and answers with a single-use link, returned once. An account the " +
			"directory already has is adopted as it is, with no link.",
		Tags: []string{"People"},
	}), a.staffInvitePerson)

	huma.Register(api, adminOnly(huma.Operation{
		OperationID: "staff-set-person-admin",
		Method:      http.MethodPut,
		Path:        "/v1/staff/people/{pk}",
		Summary:     "Make somebody an administrator of the site, or not",
		Description: "Refused when it would leave nobody able to administer.",
		Tags:        []string{"People"},
	}), a.staffSetPersonAdmin)

	huma.Register(api, adminOnly(huma.Operation{
		OperationID: "staff-set-person-email",
		Method:      http.MethodPut,
		Path:        "/v1/staff/people/{pk}/email",
		Summary:     "Change the address somebody can be reached at",
		Description: "Information for the movement's organisers: nothing is sent to it and " +
			"nobody signs in with it. Empty clears it.",
		Tags: []string{"People"},
	}), a.staffSetPersonEmail)

	huma.Register(api, adminOnly(huma.Operation{
		OperationID: "staff-relink-person",
		Method:      http.MethodPost,
		Path:        "/v1/staff/people/{pk}/link",
		Summary:     "Mint a fresh way in for somebody",
		Tags:        []string{"People"},
	}), a.staffRelinkPerson)

	huma.Register(api, adminOnly(huma.Operation{
		OperationID: "staff-remove-person",
		Method:      http.MethodDelete,
		Path:        "/v1/staff/people/{pk}",
		Summary:     "Take somebody out of every one of this site's groups",
		Description: "Their account in the directory is left exactly as it was. Refused for " +
			"yourself, and for the last administrator.",
		Tags: []string{"People"},
	}), a.staffRemovePerson)

	huma.Register(api, staffOnly(huma.Operation{
		OperationID: "staff-list-users",
		Method:      http.MethodGet,
		Path:        "/v1/staff/users",
		Summary:     "The console's users, to choose from",
		Description: "Everybody in the users' or the administrators' group, for the pickers " +
			"of whoever gives roles: the site's administrators, and those of a collective or " +
			"an organisation.",
		Tags: []string{"People"},
	}), a.staffListUsers)
}

// RoleRef is one role somebody holds on a collective or an organisation.
type RoleRef struct {
	// Kind is "collective" or "organisation"; Role is "admin", "author" or
	// "member".
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

// PersonItem is one person with a role here.
type PersonItem struct {
	PK       int       `json:"pk"`
	Username string    `json:"username"`
	Name     string    `json:"name,omitempty"`
	Email    string    `json:"email,omitempty"`
	Admin    bool      `json:"admin"`
	Roles    []RoleRef `json:"roles"`

	// Self marks the administrator asking, so the controls that would lock
	// them out of their own console can be left off.
	Self bool `json:"self"`
}

// PeopleOutput is the staff list.
type PeopleOutput struct {
	Body struct {
		People []PersonItem `json:"people"`

		// AdminGroup and UsersGroup are the two global groups, named so the
		// page can say them.
		AdminGroup string `json:"admin_group"`
		UsersGroup string `json:"users_group"`

		// RecoveryReady says somebody added now can be given a way in.
		RecoveryReady bool `json:"recovery_ready"`
	}
}

// siteGroups is every group this site gives meaning to, and which entry and
// role each per-entry group stands for.
type siteGroups struct {
	admin string
	users string
	roles map[string]RoleRef
}

func (g siteGroups) names() []string {
	names := []string{g.admin, g.users}
	var perEntry []string
	for name := range g.roles {
		perEntry = append(perEntry, name)
	}
	sort.Strings(perEntry)
	return append(names, perEntry...)
}

func (a *API) siteGroups(ctx context.Context) (siteGroups, error) {
	out := siteGroups{admin: a.currentAdminGroup(), users: a.usersGroup(), roles: map[string]RoleRef{}}

	collectives, _, err := a.store.ListCollectives(ctx, store.CollectiveQuery{Page: store.Page{Limit: 500}})
	if err != nil {
		return siteGroups{}, err
	}
	for _, c := range collectives {
		if c.AdminGroup != "" {
			out.roles[c.AdminGroup] = RoleRef{Kind: "collective", ID: c.ID, Name: c.Name, Role: "admin"}
		}
		if c.AuthorGroup != "" {
			out.roles[c.AuthorGroup] = RoleRef{Kind: "collective", ID: c.ID, Name: c.Name, Role: "author"}
		}
	}

	organisations, _, err := a.store.ListOrganisations(ctx, store.OrganisationQuery{Page: store.Page{Limit: 5000}})
	if err != nil {
		return siteGroups{}, err
	}
	for _, o := range organisations {
		if o.AdminGroup != "" {
			out.roles[o.AdminGroup] = RoleRef{Kind: "organisation", ID: o.ID, Name: o.Name, Role: "admin"}
		}
		if o.MemberGroup != "" {
			out.roles[o.MemberGroup] = RoleRef{Kind: "organisation", ID: o.ID, Name: o.Name, Role: "member"}
		}
	}
	return out, nil
}

// peopleClient is the directory for a people route, refusing when there is
// none: managing people needs one, and without it there is nothing to manage.
func (a *API) peopleClient() (*authentik.Client, error) {
	client, _ := a.directory.connected()
	if client == nil {
		return nil, huma.Error503ServiceUnavailable(
			"no identity provider is configured: run the console's setup wizard first")
	}
	return client, nil
}

// directoryRefusal turns a directory error into an answer: a revoked token is
// the operator's problem, anything else is the directory's.
func directoryRefusal(err error, doing string) error {
	if errors.Is(err, authentik.ErrTokenRefused) {
		log.Error().Err(err).Msg("the directory refused the backend's token")
		return huma.Error503ServiceUnavailable(
			"the console's access to the identity provider has been revoked — an operator has to run its setup again")
	}
	log.Error().Err(err).Msg("the directory refused: " + doing)
	return huma.Error502BadGateway(doing + ": " + err.Error())
}

func (a *API) staffListPeople(ctx context.Context, _ *struct{}) (*PeopleOutput, error) {
	who, err := mustAdmin(ctx)
	if err != nil {
		return nil, err
	}
	client, err := a.peopleClient()
	if err != nil {
		return nil, err
	}
	groups, err := a.siteGroups(ctx)
	if err != nil {
		log.Error().Err(err).Msg("cannot list the site's groups for the people page")
		return nil, huma.Error500InternalServerError("cannot list the people")
	}

	byPK := map[int]*PersonItem{}
	for _, name := range groups.names() {
		members, err := client.UsersInGroup(ctx, name)
		if err != nil {
			return nil, directoryRefusal(err, "cannot read who is in "+name)
		}
		for _, person := range members {
			row, seen := byPK[person.PK]
			if !seen {
				row = &PersonItem{
					PK: person.PK, Username: person.Username, Name: person.Name, Email: person.Email,
					Roles: []RoleRef{},
					Self:  who.Username != "" && who.Username == person.Username,
				}
				byPK[person.PK] = row
			}
			if name == groups.admin {
				row.Admin = true
			}
			if role, ok := groups.roles[name]; ok {
				row.Roles = append(row.Roles, role)
			}
		}
	}

	out := &PeopleOutput{}
	out.Body.AdminGroup, out.Body.UsersGroup = groups.admin, groups.users
	out.Body.People = make([]PersonItem, 0, len(byPK))
	for _, row := range byPK {
		sort.Slice(row.Roles, func(i, j int) bool {
			if row.Roles[i].Name != row.Roles[j].Name {
				return row.Roles[i].Name < row.Roles[j].Name
			}
			return row.Roles[i].Role < row.Roles[j].Role
		})
		out.Body.People = append(out.Body.People, *row)
	}
	// By name, so the list reads the same way twice running.
	sort.Slice(out.Body.People, func(i, j int) bool {
		left, right := out.Body.People[i], out.Body.People[j]
		if left.Name != right.Name {
			return left.Name < right.Name
		}
		return left.Username < right.Username
	})

	if found, err := client.Check(ctx); err == nil {
		out.Body.RecoveryReady = found.HasRecoveryFlow
	} else {
		log.Warn().Err(err).Msg("cannot check whether the directory can onboard anybody")
		out.Body.RecoveryReady = true
	}
	return out, nil
}

// InviteInput adds somebody.
type InviteInput struct {
	Body struct {
		Username string `json:"username"`
		Name     string `json:"name"`
		Email    string `json:"email,omitempty" doc:"where the movement can reach them; optional, and never sent to"`
		Admin    bool   `json:"admin"`
	}
}

// LinkOutput is a way in, shown once.
type LinkOutput struct {
	Body struct {
		Username string `json:"username"`
		Link     string `json:"link,omitempty"`

		// Adopted says the account already existed and was given its roles as
		// it is: it signs in with what it already has, so no link was minted.
		Adopted bool `json:"adopted"`
	}
}

func (a *API) staffInvitePerson(ctx context.Context, in *InviteInput) (*LinkOutput, error) {
	who, err := mustAdmin(ctx)
	if err != nil {
		return nil, err
	}
	client, err := a.peopleClient()
	if err != nil {
		return nil, err
	}

	username := strings.TrimSpace(in.Body.Username)
	name, err := cleanLine("the name", in.Body.Name, maxNameRunes)
	if err != nil {
		return nil, err
	}
	if username == "" || name == "" {
		return nil, huma.Error422UnprocessableEntity("a username and a name are both needed")
	}
	email, err := cleanEmail("the email", in.Body.Email)
	if err != nil {
		return nil, err
	}

	// Nothing is created until the directory can hand the account over. An
	// account that exists already needs nothing handed over.
	if _, err := client.UserByUsername(ctx, username); errors.Is(err, authentik.ErrNotFound) {
		if found, err := client.Check(ctx); err == nil && !found.HasRecoveryFlow {
			return nil, huma.Error409Conflict("nobody was created: the identity provider has no recovery flow, " +
				"so there would be no way to hand the account over")
		}
	}

	wanted := []string{a.usersGroup()}
	if in.Body.Admin {
		wanted = append(wanted, a.currentAdminGroup())
	}
	groupPKs := map[string]string{}
	var pks []string
	for _, group := range wanted {
		found, err := client.EnsureGroup(ctx, group)
		if err != nil {
			return nil, directoryRefusal(err, "cannot find the group "+group)
		}
		groupPKs[group] = found.PK
		pks = append(pks, found.PK)
	}

	member, err := client.EnsureMember(ctx, authentik.UserSpec{Username: username, Name: name, Email: email, Groups: pks}, groupPKs)
	if errors.Is(err, authentik.ErrInactive) {
		return nil, huma.Error409Conflict(username + " exists in the identity provider and is deactivated: " +
			"reactivate the account there first — this site does not overrule that")
	}
	if err != nil {
		return nil, directoryRefusal(err, "cannot add "+username)
	}
	a.directory.forget(username)

	out := &LinkOutput{}
	out.Body.Username = username
	if member.Adopted && email != "" && member.Email == "" {
		// An adopted account keeps everything it has. An address it does
		// not have takes nothing from it, and is what the administrator
		// came to say.
		if err := client.SetEmail(ctx, member.PK, email); err != nil {
			return nil, directoryRefusal(err, username+" was given access, and the email could not be stored")
		}
	}
	if member.Adopted {
		// They can already sign in, with credentials that are theirs. A
		// recovery link would let whoever holds it replace those, so none is
		// minted unasked; "new way in" on their row does it deliberately.
		log.Warn().Str("by", who.Username).Str("username", username).Strs("joined", member.Joined).
			Msg("an existing account was given access to the console")
		out.Body.Adopted = true
		return out, nil
	}
	link, err := client.RecoveryLink(ctx, member.PK)
	if err != nil {
		// The account exists and cannot be handed over: said with the account
		// named, so the administrator mints a link rather than making a second
		// account for the same person.
		return nil, directoryRefusal(err, username+" was created, and no way in could be made")
	}

	log.Warn().Str("by", who.Username).Str("username", username).Bool("admin", in.Body.Admin).
		Msg("somebody was added to the console")
	out.Body.Link = link
	return out, nil
}

// SetAdminInput makes somebody an administrator of the site, or not.
type SetAdminInput struct {
	PK   int `path:"pk"`
	Body struct {
		Username string `json:"username"`
		Admin    bool   `json:"admin"`
	}
}

// DoneRolesOutput says it worked.
type DoneRolesOutput struct {
	Body struct {
		Done bool `json:"done"`
	}
}

func doneRoles() *DoneRolesOutput {
	out := &DoneRolesOutput{}
	out.Body.Done = true
	return out
}

func (a *API) staffSetPersonAdmin(ctx context.Context, in *SetAdminInput) (*DoneRolesOutput, error) {
	who, err := mustAdmin(ctx)
	if err != nil {
		return nil, err
	}
	client, err := a.peopleClient()
	if err != nil {
		return nil, err
	}
	adminGroup := a.currentAdminGroup()
	if !in.Body.Admin {
		if err := a.notTheLastAdmin(ctx, client, adminGroup, in.PK); err != nil {
			return nil, err
		}
	}
	if err := a.setMembership(ctx, client, adminGroup, in.PK, in.Body.Admin); err != nil {
		return nil, err
	}
	// An administrator stepping down stays a user, rather than losing the
	// console altogether as a side effect.
	if !in.Body.Admin {
		if err := a.setMembership(ctx, client, a.usersGroup(), in.PK, true); err != nil {
			return nil, err
		}
	}
	a.directory.forget(in.Body.Username)

	log.Warn().Str("by", who.Username).Str("username", in.Body.Username).Int("person", in.PK).
		Bool("admin", in.Body.Admin).Msg("somebody's administration of the site was changed")
	return doneRoles(), nil
}

// setMembership puts somebody in a group or takes them out, creating the
// group when somebody has to be put in it.
func (a *API) setMembership(ctx context.Context, client *authentik.Client, name string, pk int, want bool) error {
	group, err := client.GroupByName(ctx, name)
	if errors.Is(err, authentik.ErrNotFound) {
		if !want {
			return nil
		}
		group, err = client.EnsureGroup(ctx, name)
	}
	if err != nil {
		return directoryRefusal(err, "cannot find the group "+name)
	}
	if want {
		err = client.AddToGroup(ctx, group.PK, pk)
	} else {
		err = client.RemoveFromGroup(ctx, group.PK, pk)
	}
	if err != nil {
		return directoryRefusal(err, "cannot change "+name)
	}
	return nil
}

// notTheLastAdmin refuses a change that would leave nobody able to administer
// — the same shape as an account's last passkey.
func (a *API) notTheLastAdmin(ctx context.Context, client *authentik.Client, adminGroup string, pk int) error {
	admins, err := client.UsersInGroup(ctx, adminGroup)
	if err != nil {
		return directoryRefusal(err, "cannot check the other administrators")
	}
	isAdmin, others := false, 0
	for _, person := range admins {
		if person.PK == pk {
			isAdmin = true
		} else if person.IsActive {
			others++
		}
	}
	if isAdmin && others == 0 {
		return huma.Error409Conflict("that is the only administrator left — make somebody else one first")
	}
	return nil
}

// SetEmailInput changes the address somebody can be reached at.
type SetEmailInput struct {
	PK   int `path:"pk"`
	Body struct {
		Username string `json:"username"`
		Email    string `json:"email"`
	}
}

func (a *API) staffSetPersonEmail(ctx context.Context, in *SetEmailInput) (*DoneRolesOutput, error) {
	who, err := mustAdmin(ctx)
	if err != nil {
		return nil, err
	}
	email, err := cleanEmail("the email", in.Body.Email)
	if err != nil {
		return nil, err
	}
	client, err := a.peopleClient()
	if err != nil {
		return nil, err
	}
	// Only somebody with a role here: the directory is shared with other
	// applications, and this site has no business editing their people.
	if err := a.holdsARole(ctx, client, in.PK); err != nil {
		return nil, err
	}
	if err := client.SetEmail(ctx, in.PK, email); err != nil {
		return nil, directoryRefusal(err, "cannot store the email")
	}

	log.Info().Str("by", who.Username).Str("username", in.Body.Username).Int("person", in.PK).
		Bool("cleared", email == "").Msg("the address somebody can be reached at was changed")
	return doneRoles(), nil
}

// holdsARole refuses anybody this site has not given a role: its users and
// administrators are the people it may change anything about.
func (a *API) holdsARole(ctx context.Context, client *authentik.Client, pk int) error {
	for _, group := range []string{a.usersGroup(), a.currentAdminGroup()} {
		members, err := client.UsersInGroup(ctx, group)
		if err != nil {
			return directoryRefusal(err, "cannot read who is in "+group)
		}
		for _, person := range members {
			if person.PK == pk {
				return nil
			}
		}
	}
	return huma.Error404NotFound("nobody with that key uses this console")
}

// RelinkInput mints a fresh way in.
type RelinkInput struct {
	PK   int `path:"pk"`
	Body struct {
		Username string `json:"username"`
	}
}

func (a *API) staffRelinkPerson(ctx context.Context, in *RelinkInput) (*LinkOutput, error) {
	who, err := mustAdmin(ctx)
	if err != nil {
		return nil, err
	}
	client, err := a.peopleClient()
	if err != nil {
		return nil, err
	}
	link, err := client.RecoveryLink(ctx, in.PK)
	if err != nil {
		return nil, directoryRefusal(err, "no way in could be made")
	}

	log.Warn().Str("by", who.Username).Str("username", in.Body.Username).Int("person", in.PK).
		Msg("a fresh way in was minted for somebody")
	out := &LinkOutput{}
	out.Body.Username, out.Body.Link = in.Body.Username, link
	return out, nil
}

// PersonInput addresses one person, with their username for the guards and
// the cache.
type PersonInput struct {
	PK       int    `path:"pk"`
	Username string `query:"username" doc:"their username, for the checks against yourself"`
}

func (a *API) staffRemovePerson(ctx context.Context, in *PersonInput) (*DoneRolesOutput, error) {
	who, err := mustAdmin(ctx)
	if err != nil {
		return nil, err
	}
	client, err := a.peopleClient()
	if err != nil {
		return nil, err
	}

	// A hidden control is not a guard: the page leaves this off your own row,
	// and the refusal is here.
	if in.Username != "" && in.Username == who.Username {
		return nil, huma.Error409Conflict("you cannot remove yourself — another administrator can")
	}
	groups, err := a.siteGroups(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("cannot remove the person")
	}
	if err := a.notTheLastAdmin(ctx, client, groups.admin, in.PK); err != nil {
		return nil, err
	}

	for _, name := range groups.names() {
		if err := a.setMembership(ctx, client, name, in.PK, false); err != nil {
			return nil, err
		}
	}
	a.directory.forget(in.Username)

	log.Warn().Str("by", who.Username).Str("username", in.Username).Int("person", in.PK).
		Msg("somebody was taken out of every one of this site's groups; their account is untouched")
	return doneRoles(), nil
}

// UserItem is a console user, to choose from.
type UserItem struct {
	PK       int    `json:"pk"`
	Username string `json:"username"`
	Name     string `json:"name,omitempty"`
	Email    string `json:"email,omitempty"`
}

// UsersOutput is the console's users.
type UsersOutput struct {
	Body struct {
		Users []UserItem `json:"users"`
	}
}

// givesRoles reports whether somebody may give any role: the site's
// administrators, and those of any collective or organisation.
func (a *API) givesRoles(ctx context.Context, who *staff) (bool, error) {
	if who.Admin {
		return true, nil
	}
	collectives, _, err := a.store.ListCollectives(ctx, store.CollectiveQuery{Groups: append([]string{}, who.Groups...)})
	if err != nil {
		return false, err
	}
	for _, c := range collectives {
		if who.administers(c) {
			return true, nil
		}
	}
	organisations, err := a.store.OrganisationsByGroups(ctx, who.Groups)
	if err != nil {
		return false, err
	}
	for _, o := range organisations {
		if who.administersOrganisation(o) {
			return true, nil
		}
	}
	return false, nil
}

func (a *API) staffListUsers(ctx context.Context, _ *struct{}) (*UsersOutput, error) {
	who, err := mustStaff(ctx)
	if err != nil {
		return nil, err
	}
	allowed, err := a.givesRoles(ctx, who)
	if err != nil {
		log.Error().Err(err).Msg("cannot read what an editor administers")
		return nil, huma.Error500InternalServerError("cannot list the users")
	}
	if !allowed {
		return nil, huma.Error403Forbidden("only somebody who gives roles may list the console's users")
	}
	client, err := a.peopleClient()
	if err != nil {
		return nil, err
	}

	seen := map[int]bool{}
	out := &UsersOutput{}
	out.Body.Users = []UserItem{}
	for _, group := range []string{a.usersGroup(), a.currentAdminGroup()} {
		members, err := client.UsersInGroup(ctx, group)
		if err != nil {
			return nil, directoryRefusal(err, "cannot read who is in "+group)
		}
		for _, person := range members {
			if seen[person.PK] || !person.IsActive {
				continue
			}
			seen[person.PK] = true
			out.Body.Users = append(out.Body.Users, UserItem{
				PK: person.PK, Username: person.Username, Name: person.Name, Email: person.Email,
			})
		}
	}
	sortUsers(out.Body.Users)
	return out, nil
}

// sortUsers orders by name, then username.
func sortUsers(users []UserItem) {
	slices.SortFunc(users, func(x, y UserItem) int {
		if c := strings.Compare(x.Name, y.Name); c != 0 {
			return c
		}
		return strings.Compare(x.Username, y.Username)
	})
}
