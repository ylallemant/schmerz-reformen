package backend

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/cache"
	"github.com/ylallemant/schmerz-reformen/internal/models"
	"github.com/ylallemant/schmerz-reformen/internal/store"
)

func (a *API) registerMapRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-map",
		Method:      http.MethodGet,
		Path:        "/v1/map",
		Summary:     "Read everything a map viewport shows",
		Description: "Topics, upcoming actions, collectives and the organisations published " +
			"collectives list, pinned inside `bounds`, in one answer, with the totals of each " +
			"across the whole site. The totals never shrink with the viewport: how many there " +
			"are is the point of putting them on one map.",
		Tags: []string{"Map"},
	}, a.readMap)
}

// MapOutput is one viewport.
type MapOutput struct {
	Body struct {
		Topics        []TopicItem           `json:"topics"`
		Actions       []ActionItem          `json:"actions"`
		Collectives   []CollectiveItem      `json:"collectives"`
		Organisations []MapOrganisationItem `json:"organisations"`

		Totals MapTotals `json:"totals"`
	}
}

// MapOrganisationItem is an organisation as the public map shows it: what it
// is, where, and the published collectives it is part of — which is how a
// reader reaches it, since an organisation has no page of its own.
//
// Not OrganisationItem, which is the console's and carries the groups that
// decide who runs it: nothing a reader has any use for.
type MapOrganisationItem struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Kind      string          `json:"kind"`
	Website   string          `json:"website,omitempty"`
	LogoID    string          `json:"logo_id,omitempty"`
	Place     string          `json:"place,omitempty"`
	Latitude  float64         `json:"latitude"`
	Longitude float64         `json:"longitude"`
	In        []CollectiveRef `json:"collectives"`
}

// MapTotals is how much there is, everywhere.
type MapTotals struct {
	Topics      int64 `json:"topics"`
	Actions     int64 `json:"actions"`
	Collectives int64 `json:"collectives"`

	// Organisations counts the organisations a published collective lists.
	Organisations int64 `json:"organisations"`

	// Amount is the sum of the money at stake across every published topic
	// that gave a figure, in whole euros.
	//
	// It is a sum of what collectives reported, not an audited total: two
	// alliances describing the same cut would count it twice. It is shown as
	// "at least what has been documented here", and the page says so.
	Amount int64 `json:"amount"`
}

// mapListing is what the cache holds for one viewport.
type mapListing struct {
	topics      topicListing
	actions     actionListing
	collectives collectiveListing

	// organisations are those a published collective lists, in view, each
	// with the published collectives that list it.
	organisations     []models.Organisation
	organisationsIn   map[string][]models.Collective
	organisationTotal int64

	amount int64
}

// mapLimit bounds each layer of one viewport. Past a few hundred markers a map
// is a smear whatever is drawn, and the cluster plugin has done its work long
// before that; a reader who wants the rest zooms in, which is a new question.
const mapLimit = 400

func (a *API) readMap(ctx context.Context, in *BoundsInput) (*MapOutput, error) {
	box, err := optionalBounds(in.Bounds)
	if err != nil {
		return nil, err
	}

	// "Upcoming" moves, so the key carries the minute. See listActions.
	now := time.Now().UTC().Truncate(time.Minute)
	key := cache.Keyed("map", in.Bounds, now.Format(time.RFC3339))

	listing, err := cache.Fetch(a.cache, key, cache.Map, func() (mapListing, error) {
		published := []models.PublishStatus{models.StatusPublished}

		topics, topicTotal, err := a.store.ListTopics(ctx, store.TopicQuery{
			Statuses: published, Bounds: box, Page: store.Page{Limit: mapLimit},
		})
		if err != nil {
			return mapListing{}, err
		}

		actions, err := a.readActions(ctx, store.ActionQuery{
			Statuses: published, From: now, Bounds: box, Page: store.Page{Limit: mapLimit},
		})
		if err != nil {
			return mapListing{}, err
		}

		collectives, collectiveTotal, err := a.store.ListCollectives(ctx, store.CollectiveQuery{
			Statuses: published, Bounds: box, Page: store.Page{Limit: mapLimit},
		})
		if err != nil {
			return mapListing{}, err
		}

		// Only what a published collective lists: an organisation is public
		// as somebody's member and in no other way.
		organisations, organisationTotal, err := a.store.ListOrganisations(ctx, store.OrganisationQuery{
			Listed: true, Bounds: box, Page: store.Page{Limit: mapLimit},
		})
		if err != nil {
			return mapListing{}, err
		}
		organisationIDs := make([]string, 0, len(organisations))
		for _, organisation := range organisations {
			organisationIDs = append(organisationIDs, organisation.ID)
		}
		organisationsIn, err := a.store.PublishedCollectivesOf(ctx, organisationIDs)
		if err != nil {
			return mapListing{}, err
		}

		amount, err := a.store.TotalAmount(ctx)
		if err != nil {
			// A figure in the header is not worth an empty map.
			log.Error().Err(err).Msg("cannot sum the amounts at stake")
		}

		ownerIDs := make([]string, 0, len(topics))
		for _, topic := range topics {
			ownerIDs = append(ownerIDs, topic.CollectiveID)
		}
		return mapListing{
			topics:      topicListing{topics: topics, owners: a.ownersOf(ctx, ownerIDs), total: topicTotal},
			actions:     actions,
			collectives: collectiveListing{collectives: collectives, total: collectiveTotal},

			organisations:     organisations,
			organisationsIn:   organisationsIn,
			organisationTotal: organisationTotal,

			amount: amount,
		}, nil
	})
	if err != nil {
		log.Error().Err(err).Msg("cannot read the map")
		return nil, huma.Error500InternalServerError("cannot read the map")
	}

	out := &MapOutput{}
	out.Body.Totals = MapTotals{
		Topics:        listing.topics.total,
		Actions:       listing.actions.total,
		Collectives:   listing.collectives.total,
		Organisations: listing.organisationTotal,
		Amount:        listing.amount,
	}

	visible := listing.topics.visible()
	out.Body.Topics = make([]TopicItem, 0, len(visible.topics))
	for _, topic := range visible.topics {
		item := toTopicItem(topic, visible.owners[topic.CollectiveID])
		// A marker's popup shows the summary; the body is a page's worth of
		// text multiplied by every pin in view.
		item.Body = ""
		out.Body.Topics = append(out.Body.Topics, item)
	}

	out.Body.Actions = a.actionItems(ctx, listing.actions.visible())
	for i := range out.Body.Actions {
		out.Body.Actions[i].Description = ""
	}

	out.Body.Collectives = make([]CollectiveItem, 0, len(listing.collectives.collectives))
	for _, collective := range listing.collectives.collectives {
		item := toCollectiveItem(collective)
		item.Description = ""
		out.Body.Collectives = append(out.Body.Collectives, item)
	}

	out.Body.Organisations = make([]MapOrganisationItem, 0, len(listing.organisations))
	for _, organisation := range listing.organisations {
		// Placed, because a marker needs a point. One with no place is still
		// counted, and still on every collective page that lists it.
		if !organisation.Location.Placed() {
			continue
		}
		item := MapOrganisationItem{
			ID: organisation.ID, Name: organisation.Name, Kind: string(organisation.Kind),
			Website: organisation.Website, LogoID: organisation.LogoID,
			Place:    organisation.Location.Label,
			Latitude: organisation.Location.Latitude, Longitude: organisation.Location.Longitude,
			In: []CollectiveRef{},
		}
		for _, collective := range listing.organisationsIn[organisation.ID] {
			item.In = append(item.In, CollectiveRef{ID: collective.ID, Name: collective.Name, Slug: collective.Slug})
		}
		out.Body.Organisations = append(out.Body.Organisations, item)
	}
	return out, nil
}
