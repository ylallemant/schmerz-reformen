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

// The people who may use the console, as the directory knows them: who
// administers, and which collectives each edits for.
//
// Administrators only. Every answer is read from Authentik as it is now — a
// page that cached its staff list would be one where somebody removed in the
// directory still appears to hold a role, which is the confusion this page
// exists to clear up.
//
// # It manages roles, never people
//
// This site provisioned itself into somebody else's directory. It creates
// accounts when asked to and puts them in its own groups, and that is the
// whole of what it does to the directory: removing somebody takes them out of
// this site's groups and leaves their account exactly as it was. Their
// directory may hold their mail and every other application they use.
//
// # No password, no email
//
// A person is handed a single-use recovery link, which an administrator
// passes on themselves. Authentik has no flag that forces a password change at
// next sign-in, so a temporary password would be a credential the
// administrator knows that outlives the first login; a link expires, works
// once, and ends with the person setting up their own passkey.
func (a *API) registerPeopleRoutes(api huma.API) {
	huma.Register(api, adminOnly(huma.Operation{
		OperationID: "staff-list-people",
		Method:      http.MethodGet,
		Path:        "/v1/staff/people",
		Summary:     "List who may use the console",
		Description: "Everybody in the administrators' group or in a collective's group, read " +
			"from the identity provider now.",
		Tags: []string{"People"},
	}), a.staffListPeople)

	huma.Register(api, adminOnly(huma.Operation{
		OperationID: "staff-invite-person",
		Method:      http.MethodPost,
		Path:        "/v1/staff/people",
		Summary:     "Add somebody, and mint their way in",
		Description: "Creates the account with its roles at once and answers with a single-use " +
			"link. Nothing is emailed and no password is set; the link is returned once and " +
			"kept nowhere.",
		Tags: []string{"People"},
	}), a.staffInvitePerson)

	huma.Register(api, adminOnly(huma.Operation{
		OperationID: "staff-set-person-roles",
		Method:      http.MethodPut,
		Path:        "/v1/staff/people/{pk}",
		Summary:     "Change what somebody may do",
		Description: "Refused when it would leave nobody able to administer.",
		Tags:        []string{"People"},
	}), a.staffSetPersonRoles)

	huma.Register(api, adminOnly(huma.Operation{
		OperationID: "staff-relink-person",
		Method:      http.MethodPost,
		Path:        "/v1/staff/people/{pk}/link",
		Summary:     "Mint a fresh way in for somebody",
		Description: "For an invitation that expired or was mislaid. A new single-use link, " +
			"returned once.",
		Tags: []string{"People"},
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
}

// PeopleCollective is a collective somebody can be made an editor of.
type PeopleCollective struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Group string `json:"group"`
}

// PersonItem is one person with a role here.
type PersonItem struct {
	PK       int    `json:"pk"`
	Username string `json:"username"`
	Name     string `json:"name,omitempty"`
	Admin    bool   `json:"admin"`

	// Collectives are the ones they edit for, by identifier.
	Collectives []string `json:"collectives"`

	// Self marks the administrator asking, so the controls that would lock
	// them out of their own console can be left off.
	Self bool `json:"self"`
}

// PeopleOutput is the staff list.
type PeopleOutput struct {
	Body struct {
		People      []PersonItem       `json:"people"`
		Collectives []PeopleCollective `json:"collectives"`

		// AdminGroup is the administrators' group, named so the page can say
		// it.
		AdminGroup string `json:"admin_group"`

		// RecoveryReady says somebody added now can be given a way in.
		RecoveryReady bool `json:"recovery_ready"`
	}
}

// managedGroups is every group this site gives meaning to: the
// administrators', and each collective's.
type managedGroups struct {
	admin       string
	collectives []PeopleCollective
}

func (m managedGroups) names() []string {
	names := []string{m.admin}
	for _, collective := range m.collectives {
		if !slices.Contains(names, collective.Group) {
			names = append(names, collective.Group)
		}
	}
	return names
}

func (a *API) managedGroups(ctx context.Context) (managedGroups, error) {
	collectives, _, err := a.store.ListCollectives(ctx, store.CollectiveQuery{Page: store.Page{Limit: 500}})
	if err != nil {
		return managedGroups{}, err
	}
	out := managedGroups{admin: a.currentAdminGroup()}
	for _, collective := range collectives {
		if collective.AuthGroup != "" {
			out.collectives = append(out.collectives, PeopleCollective{
				ID: collective.ID, Name: collective.Name, Group: collective.AuthGroup,
			})
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
	groups, err := a.managedGroups(ctx)
	if err != nil {
		log.Error().Err(err).Msg("cannot list the collectives for the people page")
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
					PK: person.PK, Username: person.Username, Name: person.Name,
					Collectives: []string{},
					Self:        who.Username != "" && who.Username == person.Username,
				}
				byPK[person.PK] = row
			}
			if name == groups.admin {
				row.Admin = true
			}
			for _, collective := range groups.collectives {
				if collective.Group == name && !slices.Contains(row.Collectives, collective.ID) {
					row.Collectives = append(row.Collectives, collective.ID)
				}
			}
		}
	}

	out := &PeopleOutput{}
	out.Body.AdminGroup = groups.admin
	out.Body.Collectives = groups.collectives
	if out.Body.Collectives == nil {
		out.Body.Collectives = []PeopleCollective{}
	}
	out.Body.People = make([]PersonItem, 0, len(byPK))
	for _, row := range byPK {
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
		// Not worth failing the page over; the warning is a nicety.
		log.Warn().Err(err).Msg("cannot check whether the directory can onboard anybody")
		out.Body.RecoveryReady = true
	}
	return out, nil
}

// RolesFields is what somebody may do: administer, and edit for these
// collectives.
type RolesFields struct {
	Admin       bool     `json:"admin"`
	Collectives []string `json:"collectives,omitempty" doc:"identifiers of the collectives they edit for"`
}

// wantedGroups resolves roles to group names, refusing a collective that does
// not exist or has no group.
func (groups managedGroups) wanted(roles RolesFields) ([]string, error) {
	var names []string
	if roles.Admin {
		names = append(names, groups.admin)
	}
	for _, id := range roles.Collectives {
		found := false
		for _, collective := range groups.collectives {
			if collective.ID == id {
				if !slices.Contains(names, collective.Group) {
					names = append(names, collective.Group)
				}
				found = true
			}
		}
		if !found {
			return nil, huma.Error422UnprocessableEntity(
				"one of those collectives does not exist or has no group in the identity provider")
		}
	}
	return names, nil
}

// InviteInput adds somebody.
type InviteInput struct {
	Body struct {
		Username string `json:"username"`
		Name     string `json:"name"`
		RolesFields
	}
}

// LinkOutput is a way in, shown once.
type LinkOutput struct {
	Body struct {
		Username string `json:"username"`
		Link     string `json:"link,omitempty"`

		// Adopted says the account already existed and was given the roles
		// as it is: it signs in with what it already has, so no link was
		// minted — one can be, from its row.
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

	groups, err := a.managedGroups(ctx)
	if err != nil {
		log.Error().Err(err).Msg("cannot list the collectives to invite somebody")
		return nil, huma.Error500InternalServerError("cannot add the person")
	}
	wanted, err := groups.wanted(in.Body.RolesFields)
	if err != nil {
		return nil, err
	}
	if len(wanted) == 0 {
		// Somebody with no role is not staff here, and would be an account in
		// the operator's directory this site can neither use nor explain.
		return nil, huma.Error422UnprocessableEntity("give them at least one role")
	}

	// Nothing is created until the directory can hand the account over. An
	// account that exists already needs nothing handed over: it signs in
	// with what it has.
	if _, err := client.UserByUsername(ctx, username); errors.Is(err, authentik.ErrNotFound) {
		if found, err := client.Check(ctx); err == nil && !found.HasRecoveryFlow {
			return nil, huma.Error409Conflict("nobody was created: the identity provider has no recovery flow, " +
				"so there would be no way to hand the account over")
		}
	}

	groupPKs := map[string]string{}
	var pks []string
	for _, name := range wanted {
		group, err := client.EnsureGroup(ctx, name)
		if err != nil {
			return nil, directoryRefusal(err, "cannot find the group "+name)
		}
		groupPKs[name] = group.PK
		pks = append(pks, group.PK)
	}

	member, err := client.EnsureMember(ctx, authentik.UserSpec{Username: username, Name: name, Groups: pks}, groupPKs)
	if errors.Is(err, authentik.ErrInactive) {
		return nil, huma.Error409Conflict(username + " exists in the identity provider and is deactivated: " +
			"reactivate the account there first — this site does not overrule that")
	}
	if err != nil {
		return nil, directoryRefusal(err, "cannot give "+username+" their roles")
	}
	a.directory.forget(username)
	out := &LinkOutput{}
	out.Body.Username = username
	if member.Adopted {
		// They can already sign in, with credentials that are theirs. A
		// recovery link would let whoever holds it replace those, so none is
		// minted unasked; "new way in" on their row does it deliberately.
		log.Warn().Str("by", who.Username).Str("username", username).Strs("joined", member.Joined).
			Msg("an existing account was given roles in the console")
		out.Body.Adopted = true
		return out, nil
	}
	created := member.User
	link, err := client.RecoveryLink(ctx, created.PK)
	if err != nil {
		// The account exists and cannot be handed over: said with the account
		// named, so the administrator mints a link rather than making a second
		// account for the same person.
		return nil, directoryRefusal(err, username+" was created, and no way in could be made")
	}

	log.Warn().Str("by", who.Username).Str("username", username).Strs("groups", wanted).
		Msg("somebody was added to the console")
	out.Body.Link = link
	return out, nil
}

// PersonInput addresses one person, with their username for the guards and
// the cache.
type PersonInput struct {
	PK       int    `path:"pk"`
	Username string `query:"username" doc:"their username, for the checks against yourself"`
}

// SetRolesInput changes what somebody may do.
type SetRolesInput struct {
	PK   int `path:"pk"`
	Body struct {
		Username string `json:"username"`
		RolesFields
	}
}

// DoneRolesOutput says it worked.
type DoneRolesOutput struct {
	Body struct {
		Done bool `json:"done"`
	}
}

func (a *API) staffSetPersonRoles(ctx context.Context, in *SetRolesInput) (*DoneRolesOutput, error) {
	who, err := mustAdmin(ctx)
	if err != nil {
		return nil, err
	}
	client, err := a.peopleClient()
	if err != nil {
		return nil, err
	}
	groups, err := a.managedGroups(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("cannot change the roles")
	}
	wanted, err := groups.wanted(in.Body.RolesFields)
	if err != nil {
		return nil, err
	}

	if !in.Body.Admin {
		if err := a.notTheLastAdmin(ctx, client, groups.admin, in.PK); err != nil {
			return nil, err
		}
	}

	for _, name := range groups.names() {
		group, err := client.GroupByName(ctx, name)
		if errors.Is(err, authentik.ErrNotFound) {
			if !slices.Contains(wanted, name) {
				continue
			}
			group, err = client.EnsureGroup(ctx, name)
		}
		if err != nil {
			return nil, directoryRefusal(err, "cannot find the group "+name)
		}
		if slices.Contains(wanted, name) {
			err = client.AddToGroup(ctx, group.PK, in.PK)
		} else {
			err = client.RemoveFromGroup(ctx, group.PK, in.PK)
		}
		if err != nil {
			return nil, directoryRefusal(err, "cannot change "+name)
		}
	}
	a.directory.forget(in.Body.Username)

	log.Warn().Str("by", who.Username).Str("username", in.Body.Username).Int("person", in.PK).
		Strs("groups", wanted).Msg("somebody's roles in the console were changed")
	out := &DoneRolesOutput{}
	out.Body.Done = true
	return out, nil
}

// notTheLastAdmin refuses a change that would leave nobody able to administer
// — the same shape as a collective's last admin or an account's last passkey.
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
	groups, err := a.managedGroups(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("cannot remove the person")
	}
	if err := a.notTheLastAdmin(ctx, client, groups.admin, in.PK); err != nil {
		return nil, err
	}

	for _, name := range groups.names() {
		group, err := client.GroupByName(ctx, name)
		if errors.Is(err, authentik.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, directoryRefusal(err, "cannot find the group "+name)
		}
		if err := client.RemoveFromGroup(ctx, group.PK, in.PK); err != nil {
			return nil, directoryRefusal(err, "cannot change "+name)
		}
	}
	a.directory.forget(in.Username)

	log.Warn().Str("by", who.Username).Str("username", in.Username).Int("person", in.PK).
		Msg("somebody was taken out of every one of this site's groups; their account is untouched")
	out := &DoneRolesOutput{}
	out.Body.Done = true
	return out, nil
}
