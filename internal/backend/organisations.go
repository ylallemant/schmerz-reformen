package backend

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/models"
	"github.com/ylallemant/schmerz-reformen/internal/store"
)

// Organisations are shared by every collective that lists them, so nobody
// changes one alone: creating, changing and deleting one are proposals, and a
// proposal is carried out once enough editors other than its author approve it
// (changes.go). Reading them is every editor's, because choosing a member is.

func (a *API) registerOrganisationRoutes(api huma.API) {
	huma.Register(api, staffOnly(huma.Operation{
		OperationID: "staff-list-organisations",
		Method:      http.MethodGet,
		Path:        "/v1/staff/organisations",
		Summary:     "List every organisation",
		Description: "By name, with the place and the parent of each — what the console's " +
			"picker searches as an editor types. Only organisations that exist: one whose " +
			"creation is still waiting for approval is a change, not an organisation.",
		Tags: []string{"Organisations"},
	}), a.staffListOrganisations)

	huma.Register(api, staffOnly(huma.Operation{
		OperationID: "staff-get-organisation",
		Method:      http.MethodGet,
		Path:        "/v1/staff/organisations/{id}",
		Summary:     "Read one organisation",
		Description: "With the collectives it is a member of — those this editor may see — " +
			"and the changes to it that are waiting for approval.",
		Tags: []string{"Organisations"},
	}), a.staffGetOrganisation)

	huma.Register(api, staffOnly(huma.Operation{
		OperationID: "staff-propose-organisation",
		Method:      http.MethodPost,
		Path:        "/v1/staff/organisations",
		Summary:     "Propose a new organisation",
		Description: "Answers with the change, not an organisation: it exists once enough " +
			"other editors approve it.",
		Tags: []string{"Organisations"},
	}), a.staffProposeOrganisation)

	huma.Register(api, staffOnly(huma.Operation{
		OperationID: "staff-propose-organisation-update",
		Method:      http.MethodPut,
		Path:        "/v1/staff/organisations/{id}",
		Summary:     "Propose a change to an organisation",
		Description: "Send the whole form; the change carries only the fields that differ, " +
			"and the organisation stays as it is until the change is approved.",
		Tags: []string{"Organisations"},
	}), a.staffProposeOrganisationUpdate)

	huma.Register(api, staffOnly(huma.Operation{
		OperationID: "staff-propose-organisation-deletion",
		Method:      http.MethodPost,
		Path:        "/v1/staff/organisations/{id}/deletion",
		Summary:     "Propose deleting an organisation",
		Description: "Refused while it is a member of a collective or the parent of another: " +
			"those are taken out first, where the collectives' editors see it happen.",
		Tags: []string{"Organisations"},
	}), a.staffProposeOrganisationDeletion)

	huma.Register(api, staffOnly(huma.Operation{
		OperationID: "staff-propose-organisation-logo",
		Method:      http.MethodPut,
		Path:        "/v1/staff/organisations/{id}/logo",
		Summary:     "Propose a new logo for an organisation",
		Description: "The image is the request body and its type the Content-Type header. " +
			"It is stored now and becomes the logo if the change is approved.",
		Tags: []string{"Organisations"},
	}), a.staffProposeOrganisationLogo)

	huma.Register(api, staffOnly(huma.Operation{
		OperationID: "staff-propose-organisation-logo-removal",
		Method:      http.MethodDelete,
		Path:        "/v1/staff/organisations/{id}/logo",
		Summary:     "Propose removing an organisation's logo",
		Tags:        []string{"Organisations"},
	}), a.staffProposeOrganisationLogoRemoval)
}

// OrganisationValuesItem is what an organisation says, on the wire — the
// organisation's own, or one side of a change.
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
	ID string `json:"id"`
	OrganisationValuesItem

	// Collectives and Pending are filled when one organisation is read.
	Collectives []CollectiveRef `json:"collectives,omitempty"`
	Pending     []ChangeItem    `json:"pending,omitempty"`
}

// toValuesItem renders one set of values, naming the parent from parents.
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
	if _, err := mustStaff(ctx); err != nil {
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
		out.Body.Organisations = append(out.Body.Organisations, OrganisationItem{
			ID:                     organisation.ID,
			OrganisationValuesItem: toValuesItem(organisation.OrganisationValues, byID),
		})
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

func (a *API) staffGetOrganisation(ctx context.Context, in *OrganisationIDInput) (*OrganisationOutput, error) {
	who, organisation, err := a.organisationFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}

	out := &OrganisationOutput{Body: OrganisationItem{
		ID:                     organisation.ID,
		OrganisationValuesItem: toValuesItem(organisation.OrganisationValues, a.parentsOf(ctx, organisation.OrganisationValues)),
	}}

	collectives, err := a.store.OrganisationCollectives(ctx, organisation.ID)
	if err != nil {
		log.Error().Err(err).Str("organisation", organisation.ID).Msg("cannot read where an organisation is a member")
		return nil, huma.Error500InternalServerError("cannot read the organisation")
	}
	for _, collective := range collectives {
		// A draft of somebody else's is not this editor's to learn about,
		// here any more than on its own page.
		if collective.Status.Public() || who.manages(collective) {
			out.Body.Collectives = append(out.Body.Collectives, *toCollectiveRef(collective))
		}
	}

	pending, _, err := a.store.ListChanges(ctx, store.ChangeQuery{
		Statuses:       []models.ChangeStatus{models.ChangePending},
		OrganisationID: organisation.ID,
	})
	if err != nil {
		log.Error().Err(err).Str("organisation", organisation.ID).Msg("cannot read the changes waiting for an organisation")
		return nil, huma.Error500InternalServerError("cannot read the organisation")
	}
	out.Body.Pending = a.changeItems(ctx, who, pending)
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

// ProposeOrganisationInput is a new organisation.
type ProposeOrganisationInput struct {
	Body OrganisationFields
}

func (a *API) staffProposeOrganisation(ctx context.Context, in *ProposeOrganisationInput) (*ChangeOutput, error) {
	who, err := mustStaff(ctx)
	if err != nil {
		return nil, err
	}
	values, err := a.organisationValues(ctx, in.Body, models.OrganisationValues{})
	if err != nil {
		return nil, err
	}
	return a.propose(ctx, who, models.OrganisationChange{Kind: models.ChangeCreate, After: values})
}

// ProposeOrganisationUpdateInput is a change to an organisation.
type ProposeOrganisationUpdateInput struct {
	ID   string `path:"id"`
	Body OrganisationFields
}

func (a *API) staffProposeOrganisationUpdate(ctx context.Context, in *ProposeOrganisationUpdateInput) (*ChangeOutput, error) {
	who, organisation, err := a.organisationFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	values, err := a.organisationValues(ctx, in.Body, organisation.OrganisationValues)
	if err != nil {
		return nil, err
	}
	return a.propose(ctx, who, models.OrganisationChange{
		Kind: models.ChangeUpdate, OrganisationID: organisation.ID, After: values,
	})
}

func (a *API) staffProposeOrganisationDeletion(ctx context.Context, in *OrganisationIDInput) (*ChangeOutput, error) {
	who, organisation, err := a.organisationFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	return a.propose(ctx, who, models.OrganisationChange{
		Kind: models.ChangeDelete, OrganisationID: organisation.ID,
	})
}

func (a *API) staffProposeOrganisationLogo(ctx context.Context, in *LogoInput) (*ChangeOutput, error) {
	who, organisation, err := a.organisationFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}

	media, err := a.storeImage(ctx, models.Media{OrganisationID: organisation.ID}, in.ContentType, in.RawBody)
	if err != nil {
		return nil, err
	}

	values := organisation.OrganisationValues
	values.LogoID = media.ID
	out, err := a.propose(ctx, who, models.OrganisationChange{
		Kind: models.ChangeUpdate, OrganisationID: organisation.ID, After: values,
	})
	if err != nil {
		// Proposed with nothing to show for it: the upload is nobody's.
		a.dropMedia(ctx, media.ID)
	}
	return out, err
}

func (a *API) staffProposeOrganisationLogoRemoval(ctx context.Context, in *OrganisationIDInput) (*ChangeOutput, error) {
	who, organisation, err := a.organisationFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	values := organisation.OrganisationValues
	values.LogoID = ""
	return a.propose(ctx, who, models.OrganisationChange{
		Kind: models.ChangeUpdate, OrganisationID: organisation.ID, After: values,
	})
}

// propose records a change as the editor's, and logs that they proposed it.
func (a *API) propose(ctx context.Context, who *staff, change models.OrganisationChange) (*ChangeOutput, error) {
	change.Author = who.Subject
	change.AuthorName = who.Name

	if err := a.store.ProposeChange(ctx, &change); err != nil {
		return nil, changeRefusal(err, "cannot record the proposed change")
	}

	a.audit(ctx, who, models.AuditPropose, "organisation", change.OrganisationID, "",
		string(change.Kind)+": "+changeName(change))
	log.Info().Str("change", change.ID).Str("kind", string(change.Kind)).
		Str("organisation", change.OrganisationID).Msg("a change to an organisation was proposed")
	return &ChangeOutput{Body: a.changeItems(ctx, who, []models.OrganisationChange{change})[0]}, nil
}

// changeRefusal turns what the store refused into an answer an editor can act
// on, and anything else into a failure for the log.
func changeRefusal(err error, failure string) error {
	switch {
	case errors.Is(err, store.ErrOrganisationNotFound):
		return huma.Error404NotFound("no such organisation")
	case errors.Is(err, store.ErrChangeNotFound):
		return huma.Error404NotFound("no such change")
	case errors.Is(err, store.ErrParentNotFound),
		errors.Is(err, store.ErrParentLoop),
		errors.Is(err, store.ErrNothingChanged):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, store.ErrOrganisationInUse),
		errors.Is(err, store.ErrChangeConflict),
		errors.Is(err, store.ErrChangeClosed),
		errors.Is(err, store.ErrAlreadyVoted):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, store.ErrOwnChange),
		errors.Is(err, store.ErrNotAuthor):
		return huma.Error403Forbidden(err.Error())
	}
	log.Error().Err(err).Msg(failure)
	return huma.Error500InternalServerError(failure)
}

// changeName is what a change is about, for a log line or a heading: the name
// it gives the organisation, or the one it had.
func changeName(change models.OrganisationChange) string {
	if change.After.Name != "" {
		return change.After.Name
	}
	return change.Before.Name
}
