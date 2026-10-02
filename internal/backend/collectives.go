package backend

import (
	"context"
	"errors"
	"net/http"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/cache"
	"github.com/ylallemant/schmerz-reformen/internal/models"
	"github.com/ylallemant/schmerz-reformen/internal/store"
)

func (a *API) registerCollectiveRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-collectives",
		Method:      http.MethodGet,
		Path:        "/v1/collectives",
		Summary:     "List the published collectives",
		Description: "The alliances readers can follow, by name. Pass `bounds` to get those " +
			"pinned inside a map viewport; `total` is of every published collective either way.",
		Tags: []string{"Collectives"},
	}, a.listCollectives)

	huma.Register(api, huma.Operation{
		OperationID: "get-collective",
		Method:      http.MethodGet,
		Path:        "/v1/collectives/{slug}",
		Summary:     "Read one collective",
		Description: "The collective with its member organisations, how many accounts follow it " +
			"and — for a signed-in reader — whether this one does. Never who else does.",
		Tags: []string{"Collectives"},
	}, a.getCollective)

	// --- the console ---

	huma.Register(api, staffOnly(huma.Operation{
		OperationID: "staff-list-collectives",
		Method:      http.MethodGet,
		Path:        "/v1/staff/collectives",
		Summary:     "List the collectives this editor may manage",
		Description: "Every collective for an administrator; for anybody else, those whose " +
			"group they are in. At every status, drafts included.",
		Tags: []string{"Console"},
	}), a.staffListCollectives)

	huma.Register(api, invalidates(cache.Collectives)(staffOnly(huma.Operation{
		OperationID: "staff-create-collective",
		Method:      http.MethodPost,
		Path:        "/v1/staff/collectives",
		Summary:     "Create a collective",
		Description: "Administrators only. A collective is created with the identity-provider " +
			"group whose members will manage it; from then on its content is theirs.",
		Tags: []string{"Console"},
	})), a.staffCreateCollective)

	huma.Register(api, staffOnly(huma.Operation{
		OperationID: "staff-get-collective",
		Method:      http.MethodGet,
		Path:        "/v1/staff/collectives/{id}",
		Summary:     "Read one collective as its editors see it",
		Tags:        []string{"Console"},
	}), a.staffGetCollective)

	huma.Register(api, invalidates(cache.Collectives)(staffOnly(huma.Operation{
		OperationID: "staff-save-collective",
		Method:      http.MethodPut,
		Path:        "/v1/staff/collectives/{id}",
		Summary:     "Change a collective's profile",
		Description: "Its editors may change what it says about itself. Its address and the " +
			"group that manages it are an administrator's to change: the first is in every " +
			"link already printed, and the second decides who is an editor at all.",
		Tags: []string{"Console"},
	})), a.staffSaveCollective)

	// Everything it published goes with it, so every family of cached answers
	// is now wrong.
	huma.Register(api, invalidates(cache.Collectives, cache.Topics, cache.Updates, cache.Actions)(
		staffOnly(huma.Operation{
			OperationID: "staff-delete-collective",
			Method:      http.MethodDelete,
			Path:        "/v1/staff/collectives/{id}",
			Summary:     "Delete a collective and everything it published",
			Description: "Administrators only. Topics, updates, actions, logos, and the follows " +
				"and intents readers attached to them all go. A collective that has merely " +
				"finished its work should be archived instead: its pages keep answering.",
			Tags: []string{"Console"},
		})), a.staffDeleteCollective)

	huma.Register(api, invalidates(cache.Collectives)(staffOnly(huma.Operation{
		OperationID: "staff-create-member",
		Method:      http.MethodPost,
		Path:        "/v1/staff/collectives/{id}/members",
		Summary:     "Add a member organisation",
		Tags:        []string{"Console"},
	})), a.staffCreateMember)

	huma.Register(api, invalidates(cache.Collectives)(staffOnly(huma.Operation{
		OperationID: "staff-save-member",
		Method:      http.MethodPut,
		Path:        "/v1/staff/members/{id}",
		Summary:     "Change a member organisation",
		Tags:        []string{"Console"},
	})), a.staffSaveMember)

	huma.Register(api, invalidates(cache.Collectives)(staffOnly(huma.Operation{
		OperationID: "staff-delete-member",
		Method:      http.MethodDelete,
		Path:        "/v1/staff/members/{id}",
		Summary:     "Remove a member organisation",
		Tags:        []string{"Console"},
	})), a.staffDeleteMember)
}

// CollectiveRef is a collective as something else names it: enough to say
// whose a topic or an action is, and link there.
type CollectiveRef struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Slug   string `json:"slug"`
	LogoID string `json:"logo_id,omitempty"`
}

func toCollectiveRef(c models.Collective) *CollectiveRef {
	if c.ID == "" {
		return nil
	}
	return &CollectiveRef{ID: c.ID, Name: c.Name, Slug: c.Slug, LogoID: c.LogoID}
}

// MemberItem is one organisation in a collective.
type MemberItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Website  string `json:"website,omitempty"`
	LogoID   string `json:"logo_id,omitempty"`
	Position int    `json:"position"`
}

// CollectiveItem is a collective on the wire.
type CollectiveItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Summary     string `json:"summary,omitempty"`
	Description string `json:"description,omitempty"`
	Website     string `json:"website,omitempty"`
	Contact     string `json:"contact,omitempty"`
	LogoID      string `json:"logo_id,omitempty"`
	Status      string `json:"status"`

	Place     string  `json:"place,omitempty"`
	Latitude  float64 `json:"latitude,omitempty"`
	Longitude float64 `json:"longitude,omitempty"`

	Members []MemberItem `json:"members,omitempty"`

	// Followers is how many accounts follow it. A count of accounts, not of
	// people, and nothing is ordered by it.
	Followers int64 `json:"followers"`

	// Following is whether the signed-in reader follows it.
	//
	// Sent explicitly, never omitted when false: "no" and "not asked" must not
	// be the same answer on the wire, or a client decoding into a value it has
	// used before keeps whatever the last answer was.
	Following bool `json:"following"`

	// AuthGroup is the identity-provider group that manages the collective.
	// Filled on the console's routes only; a reader is never told it.
	AuthGroup string `json:"auth_group,omitempty"`
}

func toCollectiveItem(c models.Collective) CollectiveItem {
	item := CollectiveItem{
		ID:          c.ID,
		Name:        c.Name,
		Slug:        c.Slug,
		Summary:     c.Summary,
		Description: c.Description,
		Website:     c.Website,
		Contact:     c.Contact,
		LogoID:      c.LogoID,
		Status:      string(c.Status),
		Place:       c.Location.Label,
	}
	if c.Location.Placed() {
		item.Latitude, item.Longitude = c.Location.Latitude, c.Location.Longitude
	}
	for _, member := range c.Members {
		item.Members = append(item.Members, toMemberItem(member))
	}
	return item
}

func toMemberItem(m models.CollectiveMember) MemberItem {
	return MemberItem{
		ID: m.ID, Name: m.Name, Kind: string(m.Kind),
		Website: m.Website, LogoID: m.LogoID, Position: m.Position,
	}
}

// BoundsInput narrows a listing to a map viewport.
type BoundsInput struct {
	Bounds string `query:"bounds" doc:"north,south,east,west — a map viewport; empty means everywhere"`
}

// CollectivesOutput is a listing of collectives.
type CollectivesOutput struct {
	Body struct {
		Collectives []CollectiveItem `json:"collectives"`
		Total       int64            `json:"total"`
	}
}

// collectiveListing is what the cache holds for one viewport.
type collectiveListing struct {
	collectives []models.Collective
	total       int64
}

func (a *API) listCollectives(ctx context.Context, in *BoundsInput) (*CollectivesOutput, error) {
	box, err := optionalBounds(in.Bounds)
	if err != nil {
		return nil, err
	}

	listing, err := cache.Fetch(a.cache, cache.Keyed("collectives.list", in.Bounds),
		cache.Collectives, func() (collectiveListing, error) {
			collectives, total, err := a.store.ListCollectives(ctx, store.CollectiveQuery{
				Statuses: []models.PublishStatus{models.StatusPublished},
				Bounds:   box,
			})
			return collectiveListing{collectives: collectives, total: total}, err
		})
	if err != nil {
		log.Error().Err(err).Msg("cannot list collectives")
		return nil, huma.Error500InternalServerError("cannot list the collectives")
	}

	out := &CollectivesOutput{}
	out.Body.Total = listing.total
	out.Body.Collectives = make([]CollectiveItem, 0, len(listing.collectives))
	for _, collective := range listing.collectives {
		out.Body.Collectives = append(out.Body.Collectives, toCollectiveItem(collective))
	}
	return out, nil
}

// CollectiveSlugInput addresses a collective by its public address.
type CollectiveSlugInput struct {
	Slug string `path:"slug"`
}

// CollectiveOutput is one collective.
type CollectiveOutput struct {
	Body CollectiveItem
}

func (a *API) getCollective(ctx context.Context, in *CollectiveSlugInput) (*CollectiveOutput, error) {
	collective, err := a.store.CollectiveBySlug(ctx, in.Slug)
	// A draft answers exactly as a collective that does not exist: whether
	// something is being prepared is not a reader's to learn from a status
	// code.
	if errors.Is(err, store.ErrCollectiveNotFound) || (err == nil && !collective.Status.Public()) {
		return nil, huma.Error404NotFound("no such collective")
	}
	if err != nil {
		log.Error().Err(err).Str("slug", in.Slug).Msg("cannot read a collective")
		return nil, huma.Error500InternalServerError("cannot read the collective")
	}

	out := &CollectiveOutput{Body: toCollectiveItem(collective)}
	out.Body.Followers, out.Body.Following = a.followState(ctx, models.FollowCollective, collective.ID)
	return out, nil
}

// --- the console ---

// collectiveFor loads a collective and checks the editor may manage it.
//
// One function for every route that touches a collective's content, because
// the check is the same and a route that did it slightly differently would be
// the one that got it wrong. A collective somebody may not manage answers 404
// rather than 403: an editor of one alliance has no business learning that
// another exists in draft.
func (a *API) collectiveFor(ctx context.Context, id string) (*staff, models.Collective, error) {
	who, err := mustStaff(ctx)
	if err != nil {
		return nil, models.Collective{}, err
	}

	collective, err := a.store.Collective(ctx, id)
	if errors.Is(err, store.ErrCollectiveNotFound) || (err == nil && !who.manages(collective)) {
		return nil, models.Collective{}, huma.Error404NotFound("no such collective")
	}
	if err != nil {
		log.Error().Err(err).Str("collective", id).Msg("cannot read a collective")
		return nil, models.Collective{}, huma.Error500InternalServerError("cannot read the collective")
	}
	return who, collective, nil
}

// staffCollectiveItem is a collective as an editor sees it: with the group
// that manages it.
func staffCollectiveItem(c models.Collective) CollectiveItem {
	item := toCollectiveItem(c)
	item.AuthGroup = c.AuthGroup
	return item
}

func (a *API) staffListCollectives(ctx context.Context, _ *struct{}) (*CollectivesOutput, error) {
	who, err := mustStaff(ctx)
	if err != nil {
		return nil, err
	}

	query := store.CollectiveQuery{}
	if !who.Admin {
		// Never nil for somebody who is not an administrator: a nil list
		// means "no restriction", and an editor in no group manages nothing.
		query.AuthGroups = append([]string{}, who.Groups...)
	}

	collectives, total, err := a.store.ListCollectives(ctx, query)
	if err != nil {
		log.Error().Err(err).Msg("cannot list collectives for the console")
		return nil, huma.Error500InternalServerError("cannot list the collectives")
	}

	out := &CollectivesOutput{}
	out.Body.Total = total
	out.Body.Collectives = make([]CollectiveItem, 0, len(collectives))
	for _, collective := range collectives {
		out.Body.Collectives = append(out.Body.Collectives, staffCollectiveItem(collective))
	}
	return out, nil
}

// CollectiveIDInput addresses a collective by identifier.
type CollectiveIDInput struct {
	ID string `path:"id"`
}

func (a *API) staffGetCollective(ctx context.Context, in *CollectiveIDInput) (*CollectiveOutput, error) {
	_, collective, err := a.collectiveFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	out := &CollectiveOutput{Body: staffCollectiveItem(collective)}
	out.Body.Followers, _ = a.followState(ctx, models.FollowCollective, collective.ID)
	return out, nil
}

// CollectiveFields is what an editor's form sends for a collective.
type CollectiveFields struct {
	Name        string `json:"name"`
	Slug        string `json:"slug,omitempty" doc:"the address; empty derives it from the name"`
	Summary     string `json:"summary,omitempty"`
	Description string `json:"description,omitempty"`
	Website     string `json:"website,omitempty"`
	Contact     string `json:"contact,omitempty" doc:"an email or web address the collective publishes to be reached at"`
	AuthGroup   string `json:"auth_group,omitempty" doc:"the identity-provider group whose members manage this collective"`
	Status      string `json:"status,omitempty" doc:"draft, published or archived"`

	PlaceInput
}

// CreateCollectiveInput is a new collective.
type CreateCollectiveInput struct {
	Body CollectiveFields
}

// applyCollectiveFields validates a form and writes it onto a collective.
//
// privileged says whether the caller may change the address and the managing
// group. For anybody else those two fields are ignored rather than refused: a
// form posts every field it has, and an editor saving a description must not
// be told off for a slug they did not touch.
func (a *API) applyCollectiveFields(ctx context.Context, collective *models.Collective, fields CollectiveFields, privileged bool) error {
	name, err := cleanLine("the name", fields.Name, maxNameRunes)
	if err != nil {
		return err
	}
	if name == "" {
		return huma.Error422UnprocessableEntity("a collective needs a name")
	}
	summary, err := cleanLine("the summary", fields.Summary, maxSummaryRunes)
	if err != nil {
		return err
	}
	description, err := cleanText("the description", fields.Description, maxBodyRunes)
	if err != nil {
		return err
	}
	website, err := cleanURL("the website", fields.Website)
	if err != nil {
		return err
	}
	contact, err := cleanContact(fields.Contact)
	if err != nil {
		return err
	}

	status, err := nextStatus(fields.Status, collective.Status)
	if err != nil {
		return err
	}

	location, err := a.resolvePlace(ctx, fields.PlaceInput, collective.Location)
	if err != nil {
		return err
	}

	if privileged {
		// An address left blank keeps the one there is, and only a collective
		// that has none yet takes one from its name: renaming a collective
		// must not quietly move it out from under every link already shared.
		slug := models.Slugify(fields.Slug)
		if slug == "" {
			slug = collective.Slug
		}
		if slug == "" {
			slug = models.Slugify(name)
		}
		if slug == "" {
			return huma.Error422UnprocessableEntity(
				"the address needs at least one letter or digit")
		}
		if utf8.RuneCountInString(slug) > maxSlugRunes {
			return huma.Error422UnprocessableEntity("the address is too long")
		}
		collective.Slug = slug

		group, err := cleanLine("the group", fields.AuthGroup, maxGroupRunes)
		if err != nil {
			return err
		}
		collective.AuthGroup = group
	}

	collective.Name = name
	collective.Summary = summary
	collective.Description = description
	collective.Website = website
	collective.Contact = contact
	collective.Status = status
	collective.Location = location
	return nil
}

func (a *API) staffCreateCollective(ctx context.Context, in *CreateCollectiveInput) (*CollectiveOutput, error) {
	who, err := mustAdmin(ctx)
	if err != nil {
		return nil, err
	}

	collective := &models.Collective{}
	if err := a.applyCollectiveFields(ctx, collective, in.Body, true); err != nil {
		return nil, err
	}

	err = a.store.CreateCollective(ctx, collective)
	if errors.Is(err, store.ErrSlugTaken) {
		return nil, huma.Error409Conflict("another collective already answers at that address")
	}
	if err != nil {
		log.Error().Err(err).Msg("cannot create a collective")
		return nil, huma.Error500InternalServerError("cannot create the collective")
	}

	a.audit(ctx, who, models.AuditCreate, "collective", collective.ID, collective.ID, collective.Name)
	log.Info().Str("collective", collective.ID).Str("slug", collective.Slug).
		Str("group", collective.AuthGroup).Msg("collective created")
	return &CollectiveOutput{Body: staffCollectiveItem(*collective)}, nil
}

// SaveCollectiveInput changes a collective.
type SaveCollectiveInput struct {
	ID   string `path:"id"`
	Body CollectiveFields
}

func (a *API) staffSaveCollective(ctx context.Context, in *SaveCollectiveInput) (*CollectiveOutput, error) {
	who, collective, err := a.collectiveFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	before := collective.Status

	if err := a.applyCollectiveFields(ctx, &collective, in.Body, who.Admin); err != nil {
		return nil, err
	}

	err = a.store.SaveCollective(ctx, &collective)
	if errors.Is(err, store.ErrSlugTaken) {
		return nil, huma.Error409Conflict("another collective already answers at that address")
	}
	if err != nil {
		log.Error().Err(err).Str("collective", collective.ID).Msg("cannot save a collective")
		return nil, huma.Error500InternalServerError("cannot save the collective")
	}

	a.audit(ctx, who, statusAction(before, collective.Status), "collective",
		collective.ID, collective.ID, collective.Name)
	return &CollectiveOutput{Body: staffCollectiveItem(collective)}, nil
}

// statusAction names a save for the audit log: by what it did to the status
// when it changed it, and as a plain update otherwise.
func statusAction(before, after models.PublishStatus) models.AuditAction {
	switch {
	case before == after:
		return models.AuditUpdate
	case after == models.StatusPublished:
		return models.AuditPublish
	case after == models.StatusArchived:
		return models.AuditArchive
	}
	return models.AuditUpdate
}

func (a *API) staffDeleteCollective(ctx context.Context, in *CollectiveIDInput) (*DoneOutput, error) {
	who, err := mustAdmin(ctx)
	if err != nil {
		return nil, err
	}
	collective, err := a.store.Collective(ctx, in.ID)
	if errors.Is(err, store.ErrCollectiveNotFound) {
		return nil, huma.Error404NotFound("no such collective")
	}
	if err != nil {
		log.Error().Err(err).Str("collective", in.ID).Msg("cannot read a collective")
		return nil, huma.Error500InternalServerError("cannot read the collective")
	}

	keys, err := a.store.DeleteCollective(ctx, collective.ID)
	if err != nil {
		log.Error().Err(err).Str("collective", collective.ID).Msg("cannot delete a collective")
		return nil, huma.Error500InternalServerError("cannot delete the collective")
	}
	// The rows are gone; the bytes follow. A failure here leaves an orphaned
	// blob and nothing a reader can reach, so it is logged rather than
	// reported as the deletion having failed — it did not.
	a.removeStored(ctx, keys)

	a.audit(ctx, who, models.AuditDelete, "collective", collective.ID, collective.ID, collective.Name)
	log.Info().Str("collective", collective.ID).Str("slug", collective.Slug).Msg("collective deleted")
	return done(), nil
}
