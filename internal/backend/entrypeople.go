package backend

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/authentik"
	"github.com/ylallemant/schmerz-reformen/internal/models"
)

// Who holds a role on one collective or one organisation, and who may change
// that:
//
//	collective   admins   the site's administrators
//	collective   authors  the collective's administrators
//	organisation admins   the organisation's administrators
//	organisation members  the organisation's administrators
//
// Each role is a group named when the entry was created; putting somebody in
// it is putting them in that group, in the directory.

func (a *API) registerEntryPeopleRoutes(api huma.API) {
	huma.Register(api, staffOnly(huma.Operation{
		OperationID: "staff-collective-people",
		Method:      http.MethodGet,
		Path:        "/v1/staff/collectives/{id}/people",
		Summary:     "Who administers a collective, and who writes for it",
		Tags:        []string{"People"},
	}), a.staffCollectivePeople)

	huma.Register(api, staffOnly(huma.Operation{
		OperationID: "staff-collective-grant",
		Method:      http.MethodPut,
		Path:        "/v1/staff/collectives/{id}/people/{role}/{pk}",
		Summary:     "Give somebody a role on a collective",
		Description: "`authors` by the collective's administrators; `admins` by the site's.",
		Tags:        []string{"People"},
	}), a.staffCollectiveGrant)

	huma.Register(api, staffOnly(huma.Operation{
		OperationID: "staff-collective-revoke",
		Method:      http.MethodDelete,
		Path:        "/v1/staff/collectives/{id}/people/{role}/{pk}",
		Summary:     "Take a role on a collective away from somebody",
		Tags:        []string{"People"},
	}), a.staffCollectiveRevoke)

	huma.Register(api, staffOnly(huma.Operation{
		OperationID: "staff-organisation-people",
		Method:      http.MethodGet,
		Path:        "/v1/staff/organisations/{id}/people",
		Summary:     "Who administers an organisation, and its people",
		Tags:        []string{"People"},
	}), a.staffOrganisationPeople)

	huma.Register(api, staffOnly(huma.Operation{
		OperationID: "staff-organisation-grant",
		Method:      http.MethodPut,
		Path:        "/v1/staff/organisations/{id}/people/{role}/{pk}",
		Summary:     "Give somebody a role on an organisation",
		Description: "`admins` and `members`, by the organisation's administrators.",
		Tags:        []string{"People"},
	}), a.staffOrganisationGrant)

	huma.Register(api, staffOnly(huma.Operation{
		OperationID: "staff-organisation-revoke",
		Method:      http.MethodDelete,
		Path:        "/v1/staff/organisations/{id}/people/{role}/{pk}",
		Summary:     "Take a role on an organisation away from somebody",
		Tags:        []string{"People"},
	}), a.staffOrganisationRevoke)
}

// EntryPeopleOutput is who holds the two roles on an entry.
type EntryPeopleOutput struct {
	Body struct {
		// Admins and Others are the two roles: an entry's administrators, and
		// its authors (a collective) or members (an organisation).
		Admins []UserItem `json:"admins"`
		Others []UserItem `json:"others"`

		// MayGrantAdmins says the editor asking may change the administrators
		// too, not only the others.
		MayGrantAdmins bool `json:"may_grant_admins"`
	}
}

// EntryRoleInput addresses one person and one role on an entry.
type EntryRoleInput struct {
	ID       string `path:"id"`
	Role     string `path:"role" enum:"admins,authors,members"`
	PK       int    `path:"pk"`
	Username string `query:"username" doc:"their username, so their roles are read afresh at once"`
}

func (a *API) entryPeople(ctx context.Context, adminGroup, otherGroup string, mayGrantAdmins bool) (*EntryPeopleOutput, error) {
	client, err := a.peopleClient()
	if err != nil {
		return nil, err
	}
	out := &EntryPeopleOutput{}
	if out.Body.Admins, err = a.groupPeople(ctx, client, adminGroup); err != nil {
		return nil, err
	}
	if out.Body.Others, err = a.groupPeople(ctx, client, otherGroup); err != nil {
		return nil, err
	}
	out.Body.MayGrantAdmins = mayGrantAdmins
	return out, nil
}

// groupPeople lists who is in one group, by name.
func (a *API) groupPeople(ctx context.Context, client *authentik.Client, group string) ([]UserItem, error) {
	people := []UserItem{}
	if group == "" {
		return people, nil
	}
	members, err := client.UsersInGroup(ctx, group)
	if err != nil {
		return nil, directoryRefusal(err, "cannot read who is in "+group)
	}
	for _, person := range members {
		people = append(people, UserItem{PK: person.PK, Username: person.Username, Name: person.Name, Email: person.Email})
	}
	sortUsers(people)
	return people, nil
}

func (a *API) staffCollectivePeople(ctx context.Context, in *CollectiveIDInput) (*EntryPeopleOutput, error) {
	who, collective, err := a.collectiveAdminFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	return a.entryPeople(ctx, collective.AdminGroup, collective.AuthorGroup, who.Admin)
}

// collectiveRoleGroup is the group a role on a collective is, after checking
// the editor may give it.
func (a *API) collectiveRoleGroup(ctx context.Context, in *EntryRoleInput) (*staff, models.Collective, string, error) {
	who, collective, err := a.collectiveAdminFor(ctx, in.ID)
	if err != nil {
		return nil, models.Collective{}, "", err
	}
	switch in.Role {
	case "authors":
		return who, collective, collective.AuthorGroup, nil
	case "admins":
		if !who.Admin {
			return nil, models.Collective{}, "", huma.Error403Forbidden(
				"only the site's administrators choose a collective's administrators")
		}
		return who, collective, collective.AdminGroup, nil
	}
	return nil, models.Collective{}, "", huma.Error422UnprocessableEntity("a collective has admins and authors")
}

func (a *API) staffCollectiveGrant(ctx context.Context, in *EntryRoleInput) (*DoneRolesOutput, error) {
	return a.changeCollectiveRole(ctx, in, true)
}

func (a *API) staffCollectiveRevoke(ctx context.Context, in *EntryRoleInput) (*DoneRolesOutput, error) {
	return a.changeCollectiveRole(ctx, in, false)
}

func (a *API) changeCollectiveRole(ctx context.Context, in *EntryRoleInput, want bool) (*DoneRolesOutput, error) {
	who, collective, group, err := a.collectiveRoleGroup(ctx, in)
	if err != nil {
		return nil, err
	}
	return a.changeEntryRole(ctx, who, in, group, want, collective.ID, "collective "+collective.Name)
}

func (a *API) staffOrganisationPeople(ctx context.Context, in *OrganisationIDInput) (*EntryPeopleOutput, error) {
	_, organisation, err := a.organisationAdminFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	return a.entryPeople(ctx, organisation.AdminGroup, organisation.MemberGroup, true)
}

func (a *API) staffOrganisationGrant(ctx context.Context, in *EntryRoleInput) (*DoneRolesOutput, error) {
	return a.changeOrganisationRole(ctx, in, true)
}

func (a *API) staffOrganisationRevoke(ctx context.Context, in *EntryRoleInput) (*DoneRolesOutput, error) {
	return a.changeOrganisationRole(ctx, in, false)
}

func (a *API) changeOrganisationRole(ctx context.Context, in *EntryRoleInput, want bool) (*DoneRolesOutput, error) {
	who, organisation, err := a.organisationAdminFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	var group string
	switch in.Role {
	case "admins":
		group = organisation.AdminGroup
	case "members":
		group = organisation.MemberGroup
	default:
		return nil, huma.Error422UnprocessableEntity("an organisation has admins and members")
	}
	return a.changeEntryRole(ctx, who, in, group, want, "", "organisation "+organisation.Name)
}

// changeEntryRole puts somebody in one of an entry's groups, or takes them
// out, and writes it to the log.
func (a *API) changeEntryRole(ctx context.Context, who *staff, in *EntryRoleInput, group string,
	want bool, collectiveID, entry string) (*DoneRolesOutput, error) {
	if group == "" {
		return nil, huma.Error409Conflict("this has no group to put anybody in; an administrator has to look at it")
	}
	client, err := a.peopleClient()
	if err != nil {
		return nil, err
	}
	if in.Username == "" {
		// Given from a picker, which sends only the key. The name is what
		// their cached roles are kept under, and what the log says.
		person, err := client.UserByPK(ctx, in.PK)
		if errors.Is(err, authentik.ErrNotFound) {
			return nil, huma.Error422UnprocessableEntity("there is nobody with that key in the identity provider")
		}
		if err != nil {
			return nil, directoryRefusal(err, "cannot find that person")
		}
		in.Username = person.Username
	}
	if err := a.setMembership(ctx, client, group, in.PK, want); err != nil {
		return nil, err
	}
	a.directory.forget(in.Username)

	verb := models.AuditCreate
	if !want {
		verb = models.AuditDelete
	}
	a.audit(ctx, who, verb, "role", group, collectiveID, in.Username+" — "+in.Role+" of "+entry)
	log.Warn().Str("by", who.Username).Str("username", in.Username).Str("group", group).Bool("given", want).
		Msg("a role was changed")
	return doneRoles(), nil
}
