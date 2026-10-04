package backend

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/cache"
	"github.com/ylallemant/schmerz-reformen/internal/models"
	"github.com/ylallemant/schmerz-reformen/internal/store"
)

// Organisations are shared by every collective that lists them. The site's
// administrators create and delete them; each organisation's own
// administrators — the people in its AdminGroup — edit it. Reading them is
// every console user's, because choosing a member is.

func (a *API) registerOrganisationRoutes(api huma.API) {
	huma.Register(api, staffOnly(huma.Operation{
		OperationID: "staff-list-organisations",
		Method:      http.MethodGet,
		Path:        "/v1/staff/organisations",
		Summary:     "List every organisation",
		Description: "By name, with the place and the parent of each — what the console's " +
			"picker searches as somebody types.",
		Tags: []string{"Organisations"},
	}), a.staffListOrganisations)

	huma.Register(api, staffOnly(huma.Operation{
		OperationID: "staff-get-organisation",
		Method:      http.MethodGet,
		Path:        "/v1/staff/organisations/{id}",
		Summary:     "Read one organisation",
		Description: "With the collectives it is a member of — those this editor may see — " +
			"and whether the editor administers it.",
		Tags: []string{"Organisations"},
	}), a.staffGetOrganisation)

	// Nothing cached names an organisation nobody lists yet.
	huma.Register(api, adminOnly(huma.Operation{
		OperationID: "staff-create-organisation",
		Method:      http.MethodPost,
		Path:        "/v1/staff/organisations",
		Summary:     "Create an organisation",
		Description: "Administrators only. Its slug and its two groups — administrators and " +
			"members — are fixed now.",
		Tags: []string{"Organisations"},
	}), a.staffCreateOrganisation)

	// An organisation appears in the member list of every collective that has
	// it, so every change to it is a change to those pages.
	huma.Register(api, invalidates(cache.Collectives)(staffOnly(huma.Operation{
		OperationID: "staff-save-organisation",
		Method:      http.MethodPut,
		Path:        "/v1/staff/organisations/{id}",
		Summary:     "Change an organisation",
		Description: "Its administrators, and the site's.",
		Tags:        []string{"Organisations"},
	})), a.staffSaveOrganisation)

	huma.Register(api, invalidates(cache.Collectives)(adminOnly(huma.Operation{
		OperationID: "staff-delete-organisation",
		Method:      http.MethodDelete,
		Path:        "/v1/staff/organisations/{id}",
		Summary:     "Delete an organisation",
		Description: "Administrators only. Refused while it is a member of a collective or the " +
			"parent of another: those are taken out first, where the collectives' editors see it.",
		Tags: []string{"Organisations"},
	})), a.staffDeleteOrganisation)

	huma.Register(api, invalidates(cache.Collectives)(staffOnly(huma.Operation{
		OperationID: "staff-put-organisation-logo",
		Method:      http.MethodPut,
		Path:        "/v1/staff/organisations/{id}/logo",
		Summary:     "Upload an organisation's logo",
		Description: "The image is the request body and its type the Content-Type header.",
		Tags:        []string{"Organisations"},
	})), a.staffPutOrganisationLogo)

	huma.Register(api, invalidates(cache.Collectives)(staffOnly(huma.Operation{
		OperationID: "staff-delete-organisation-logo",
		Method:      http.MethodDelete,
		Path:        "/v1/staff/organisations/{id}/logo",
		Summary:     "Remove an organisation's logo",
		Tags:        []string{"Organisations"},
	})), a.staffDeleteOrganisationLogo)
}

// OrganisationValuesItem is what an organisation says, on the wire.
type OrganisationValuesItem struct {
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

// OrganisationItem is an organisation on the wire.
type OrganisationItem struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	OrganisationValuesItem

	// AdminGroup and MemberGroup are its groups, and Administers whether the
	// editor asking is one of its administrators.
	AdminGroup  string `json:"admin_group,omitempty"`
	MemberGroup string `json:"member_group,omitempty"`
	Administers bool   `json:"administers"`

	// Collectives is filled when one organisation is read.
	Collectives []CollectiveRef `json:"collectives,omitempty"`
}

// toValuesItem renders an organisation's values, naming the parent from parents.
func toValuesItem(values models.OrganisationValues, parents map[string]models.Organisation) OrganisationValuesItem {
	item := OrganisationValuesItem{
		Name: values.Name, Kind: string(values.Kind), Website: values.Website,
		ParentID: values.ParentID, LogoID: values.LogoID, Place: values.Location.Label,
	}
	if parent, ok := parents[values.ParentID]; ok {
		item.ParentName = parent.Name
	}
	if values.Location.Placed() {
		item.Latitude, item.Longitude = values.Location.Latitude, values.Location.Longitude
	}
	return item
}

func toOrganisationItem(organisation models.Organisation, parents map[string]models.Organisation, who *staff) OrganisationItem {
	item := OrganisationItem{
		ID: organisation.ID, Slug: organisation.Slug,
		OrganisationValuesItem: toValuesItem(organisation.OrganisationValues, parents),
		AdminGroup:             organisation.AdminGroup, MemberGroup: organisation.MemberGroup,
	}
	if who != nil {
		item.Administers = who.administersOrganisation(organisation)
	}
	return item
}

// parentsOf reads the organisations a set of values names as parents, in one
// query. A parent that has gone is simply not named.
func (a *API) parentsOf(ctx context.Context, values ...models.OrganisationValues) map[string]models.Organisation {
	var ids []string
	for _, value := range values {
		if value.ParentID != "" {
			ids = append(ids, value.ParentID)
		}
	}
	parents, err := a.store.OrganisationsByID(ctx, ids)
	if err != nil {
		log.Warn().Err(err).Msg("cannot name the parent organisations")
		return map[string]models.Organisation{}
	}
	return parents
}

// OrganisationsOutput is every organisation.
type OrganisationsOutput struct {
	Body struct {
		Organisations []OrganisationItem `json:"organisations"`
		Total         int64              `json:"total"`
	}
}

func (a *API) staffListOrganisations(ctx context.Context, _ *struct{}) (*OrganisationsOutput, error) {
	who, err := mustStaff(ctx)
	if err != nil {
		return nil, err
	}

	organisations, total, err := a.store.ListOrganisations(ctx, store.OrganisationQuery{
		Page: store.Page{Limit: 5000},
	})
	if err != nil {
		log.Error().Err(err).Msg("cannot list organisations")
		return nil, huma.Error500InternalServerError("cannot list the organisations")
	}

	// Every parent is in the list already: no second query.
	byID := make(map[string]models.Organisation, len(organisations))
	for _, organisation := range organisations {
		byID[organisation.ID] = organisation
	}

	out := &OrganisationsOutput{}
	out.Body.Total = total
	out.Body.Organisations = make([]OrganisationItem, 0, len(organisations))
	for _, organisation := range organisations {
		out.Body.Organisations = append(out.Body.Organisations, toOrganisationItem(organisation, byID, who))
	}
	return out, nil
}

// OrganisationIDInput addresses an organisation.
type OrganisationIDInput struct {
	ID string `path:"id"`
}

// OrganisationOutput is one organisation.
type OrganisationOutput struct {
	Body OrganisationItem
}

// organisationFor reads an organisation for an editor.
func (a *API) organisationFor(ctx context.Context, id string) (*staff, models.Organisation, error) {
	who, err := mustStaff(ctx)
	if err != nil {
		return nil, models.Organisation{}, err
	}
	organisation, err := a.store.Organisation(ctx, id)
	if errors.Is(err, store.ErrOrganisationNotFound) {
		return nil, models.Organisation{}, huma.Error404NotFound("no such organisation")
	}
	if err != nil {
		log.Error().Err(err).Str("organisation", id).Msg("cannot read an organisation")
		return nil, models.Organisation{}, huma.Error500InternalServerError("cannot read the organisation")
	}
	return who, organisation, nil
}

// organisationAdminFor is organisationFor for what only its administrators —
// and the site's — may do.
func (a *API) organisationAdminFor(ctx context.Context, id string) (*staff, models.Organisation, error) {
	who, organisation, err := a.organisationFor(ctx, id)
	if err != nil {
		return nil, models.Organisation{}, err
	}
	if !who.administersOrganisation(organisation) {
		return nil, models.Organisation{}, huma.Error403Forbidden("only the organisation's administrators may do that")
	}
	return who, organisation, nil
}

func (a *API) staffGetOrganisation(ctx context.Context, in *OrganisationIDInput) (*OrganisationOutput, error) {
	who, organisation, err := a.organisationFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}

	out := &OrganisationOutput{Body: toOrganisationItem(organisation,
		a.parentsOf(ctx, organisation.OrganisationValues), who)}

	collectives, err := a.store.OrganisationCollectives(ctx, organisation.ID)
	if err != nil {
		log.Error().Err(err).Str("organisation", organisation.ID).Msg("cannot read where an organisation is a member")
		return nil, huma.Error500InternalServerError("cannot read the organisation")
	}
	for _, collective := range collectives {
		// A draft of somebody else's is not this editor's to learn about,
		// here any more than on its own page.
		if collective.Status.Public() || who.authors(collective) {
			out.Body.Collectives = append(out.Body.Collectives, *toCollectiveRef(collective))
		}
	}
	return out, nil
}

// OrganisationFields is what an editor's form sends for an organisation.
type OrganisationFields struct {
	Name     string `json:"name"`
	Kind     string `json:"kind,omitempty" doc:"union, party, association, initiative or other"`
	Website  string `json:"website,omitempty"`
	ParentID string `json:"parent_id,omitempty" doc:"the organisation this one is part of; empty for none"`

	PlaceInput
}

// organisationValues validates a form. previous is what the organisation says
// now — nothing, for a new one — which decides whether the pin moved, and
// supplies what the form does not carry: the logo.
func (a *API) organisationValues(ctx context.Context, fields OrganisationFields, previous models.OrganisationValues) (models.OrganisationValues, error) {
	name, err := cleanLine("the name", fields.Name, maxNameRunes)
	if err != nil {
		return models.OrganisationValues{}, err
	}
	if name == "" {
		return models.OrganisationValues{}, huma.Error422UnprocessableEntity("an organisation needs a name")
	}
	website, err := cleanURL("the website", fields.Website)
	if err != nil {
		return models.OrganisationValues{}, err
	}
	kind := models.MemberKind(strings.TrimSpace(fields.Kind))
	if kind == "" {
		kind = models.MemberOther
	}
	if !kind.Valid() {
		return models.OrganisationValues{}, huma.Error422UnprocessableEntity("unknown kind of organisation")
	}
	location, err := a.resolvePlace(ctx, fields.PlaceInput, previous.Location)
	if err != nil {
		return models.OrganisationValues{}, err
	}

	return models.OrganisationValues{
		Name:     name,
		Kind:     kind,
		Website:  website,
		ParentID: strings.TrimSpace(fields.ParentID),
		LogoID:   previous.LogoID,
		Location: location,
	}, nil
}

// parentRefusal answers a parent that does not exist or would loop.
func parentRefusal(err error) error {
	if errors.Is(err, store.ErrParentNotFound) || errors.Is(err, store.ErrParentLoop) {
		return huma.Error422UnprocessableEntity(err.Error())
	}
	log.Error().Err(err).Msg("cannot check an organisation's parent")
	return huma.Error500InternalServerError("cannot save the organisation")
}

// CreateOrganisationInput is a new organisation.
type CreateOrganisationInput struct {
	Body OrganisationFields
}

func (a *API) staffCreateOrganisation(ctx context.Context, in *CreateOrganisationInput) (*OrganisationOutput, error) {
	who, err := mustAdmin(ctx)
	if err != nil {
		return nil, err
	}
	values, err := a.organisationValues(ctx, in.Body, models.OrganisationValues{})
	if err != nil {
		return nil, err
	}
	if values.ParentID != "" {
		if err := a.store.CheckParent(ctx, "", values.ParentID); err != nil {
			return nil, parentRefusal(err)
		}
	}

	organisation := &models.Organisation{OrganisationValues: values}
	if err := a.store.CreateOrganisation(ctx, organisation, a.appName()); err != nil {
		log.Error().Err(err).Msg("cannot create an organisation")
		return nil, huma.Error500InternalServerError("cannot create the organisation")
	}
	a.ensureDirectoryGroup(ctx, organisation.AdminGroup)
	a.ensureDirectoryGroup(ctx, organisation.MemberGroup)

	a.audit(ctx, who, models.AuditCreate, "organisation", organisation.ID, "", organisation.Name)
	return &OrganisationOutput{Body: toOrganisationItem(*organisation,
		a.parentsOf(ctx, organisation.OrganisationValues), who)}, nil
}

// SaveOrganisationInput changes an organisation.
type SaveOrganisationInput struct {
	ID   string `path:"id"`
	Body OrganisationFields
}

func (a *API) staffSaveOrganisation(ctx context.Context, in *SaveOrganisationInput) (*OrganisationOutput, error) {
	who, organisation, err := a.organisationAdminFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	values, err := a.organisationValues(ctx, in.Body, organisation.OrganisationValues)
	if err != nil {
		return nil, err
	}
	if values.ParentID != organisation.ParentID {
		if err := a.store.CheckParent(ctx, organisation.ID, values.ParentID); err != nil {
			return nil, parentRefusal(err)
		}
	}

	organisation.OrganisationValues = values
	if err := a.store.SaveOrganisation(ctx, &organisation); err != nil {
		log.Error().Err(err).Str("organisation", organisation.ID).Msg("cannot save an organisation")
		return nil, huma.Error500InternalServerError("cannot save the organisation")
	}
	a.audit(ctx, who, models.AuditUpdate, "organisation", organisation.ID, "", organisation.Name)
	return &OrganisationOutput{Body: toOrganisationItem(organisation,
		a.parentsOf(ctx, organisation.OrganisationValues), who)}, nil
}

func (a *API) staffDeleteOrganisation(ctx context.Context, in *OrganisationIDInput) (*DoneOutput, error) {
	who, err := mustAdmin(ctx)
	if err != nil {
		return nil, err
	}
	deleted, err := a.store.DeleteOrganisation(ctx, in.ID)
	switch {
	case errors.Is(err, store.ErrOrganisationNotFound):
		return nil, huma.Error404NotFound("no such organisation")
	case errors.Is(err, store.ErrOrganisationInUse):
		return nil, huma.Error409Conflict(err.Error())
	case err != nil:
		log.Error().Err(err).Str("organisation", in.ID).Msg("cannot delete an organisation")
		return nil, huma.Error500InternalServerError("cannot delete the organisation")
	}
	// Its logo was its own. Its groups stay in the directory: they are the
	// operator's to clean up, and people in them may be in them on purpose.
	a.dropMedia(ctx, deleted.LogoID)
	a.audit(ctx, who, models.AuditDelete, "organisation", deleted.ID, "", deleted.Name)
	return done(), nil
}

func (a *API) staffPutOrganisationLogo(ctx context.Context, in *LogoInput) (*LogoOutput, error) {
	who, organisation, err := a.organisationAdminFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	media, err := a.storeImage(ctx, models.Media{OrganisationID: organisation.ID}, in.ContentType, in.RawBody)
	if err != nil {
		return nil, err
	}

	previous := organisation.LogoID
	organisation.LogoID = media.ID
	if err := a.store.SaveOrganisation(ctx, &organisation); err != nil {
		log.Error().Err(err).Str("organisation", organisation.ID).Msg("cannot attach a logo")
		a.dropMedia(ctx, media.ID)
		return nil, huma.Error500InternalServerError("cannot save the logo")
	}
	// Only once the new one is in place: a failed upload leaves the old one.
	a.dropMedia(ctx, previous)

	a.audit(ctx, who, models.AuditUpdate, "organisation", organisation.ID, "", organisation.Name+" (logo)")
	out := &LogoOutput{}
	out.Body.LogoID = media.ID
	return out, nil
}

func (a *API) staffDeleteOrganisationLogo(ctx context.Context, in *OrganisationIDInput) (*DoneOutput, error) {
	who, organisation, err := a.organisationAdminFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if organisation.LogoID == "" {
		return done(), nil
	}
	previous := organisation.LogoID
	organisation.LogoID = ""
	if err := a.store.SaveOrganisation(ctx, &organisation); err != nil {
		log.Error().Err(err).Str("organisation", organisation.ID).Msg("cannot detach a logo")
		return nil, huma.Error500InternalServerError("cannot remove the logo")
	}
	a.dropMedia(ctx, previous)
	a.audit(ctx, who, models.AuditUpdate, "organisation", organisation.ID, "", organisation.Name+" (logo removed)")
	return done(), nil
}
