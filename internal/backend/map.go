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
		Description: "Topics, upcoming actions and collectives pinned inside `bounds`, in one " +
			"answer, with the totals of each across the whole site. The totals never shrink " +
			"with the viewport: how many there are is the point of putting them on one map.",
		Tags: []string{"Map"},
	}, a.readMap)
}

// MapOutput is one viewport.
type MapOutput struct {
	Body struct {
		Topics      []TopicItem      `json:"topics"`
		Actions     []ActionItem     `json:"actions"`
		Collectives []CollectiveItem `json:"collectives"`

		Totals MapTotals `json:"totals"`
	}
}

// MapTotals is how much there is, everywhere.
type MapTotals struct {
	Topics      int64 `json:"topics"`
	Actions     int64 `json:"actions"`
	Collectives int64 `json:"collectives"`

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
	amount      int64
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
			amount:      amount,
		}, nil
	})
	if err != nil {
		log.Error().Err(err).Msg("cannot read the map")
		return nil, huma.Error500InternalServerError("cannot read the map")
	}

	out := &MapOutput{}
	out.Body.Totals = MapTotals{
		Topics:      listing.topics.total,
		Actions:     listing.actions.total,
		Collectives: listing.collectives.total,
		Amount:      listing.amount,
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
	return out, nil
}
