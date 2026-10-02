package backend

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/models"
	"github.com/ylallemant/schmerz-reformen/internal/store"
)

func (a *API) registerFollowRoutes(api huma.API) {
	huma.Register(api, authenticated(huma.Operation{
		OperationID: "follow",
		Method:      http.MethodPut,
		Path:        "/v1/follows/{target}/{id}",
		Summary:     "Follow a collective or a topic",
		Description: "The reader is told when it publishes something, and it appears in their " +
			"own feed. Following twice is following once.",
		Tags: []string{"Following"},
	}), a.follow)

	huma.Register(api, authenticated(huma.Operation{
		OperationID: "unfollow",
		Method:      http.MethodDelete,
		Path:        "/v1/follows/{target}/{id}",
		Summary:     "Stop following a collective or a topic",
		Tags:        []string{"Following"},
	}), a.unfollow)

	huma.Register(api, authenticated(huma.Operation{
		OperationID: "list-following",
		Method:      http.MethodGet,
		Path:        "/v1/accounts/me/following",
		Summary:     "List what this account follows",
		Description: "The whole list, to the one account it belongs to. Nobody else is ever " +
			"shown it: what is public about a follow is the count on the thing followed.",
		Tags: []string{"Following"},
	}), a.listFollowing)

	huma.Register(api, authenticated(huma.Operation{
		OperationID: "participate",
		Method:      http.MethodPut,
		Path:        "/v1/actions/{id}/participation",
		Summary:     "Say you intend to come to an action",
		Description: "An intent, not a registration: nobody is checked at the door. It adds one " +
			"to the count the organisers and other readers see, and the reader is told if the " +
			"action moves or is called off.",
		Tags: []string{"Following"},
	}), a.participate)

	huma.Register(api, authenticated(huma.Operation{
		OperationID: "withdraw",
		Method:      http.MethodDelete,
		Path:        "/v1/actions/{id}/participation",
		Summary:     "Withdraw the intent to come to an action",
		Tags:        []string{"Following"},
	}), a.withdraw)
}

// followState is the two facts a page shows about following: how many accounts
// do, and whether the one asking does.
//
// Neither is ever cached, and the second cannot be: it is the answer for one
// reader. Failures are logged and read as "nobody, and not you" — a page that
// refused to render because a count could not be read would be the worse
// outcome.
func (a *API) followState(ctx context.Context, target models.FollowTarget, id string) (followers int64, following bool) {
	followers, err := a.store.CountFollowers(ctx, target, id)
	if err != nil {
		log.Error().Err(err).Str("target", string(target)).Msg("cannot count followers")
	}

	if who := callerOf(ctx); who != nil {
		following, err = a.store.Follows(ctx, who.Account.ID, target, id)
		if err != nil {
			log.Error().Err(err).Msg("cannot read whether an account follows something")
		}
	}
	return followers, following
}

// FollowInput addresses something to follow.
type FollowInput struct {
	Target string `path:"target" doc:"collective or topic"`
	ID     string `path:"id"`
}

// FollowOutput says where things stand after a change.
type FollowOutput struct {
	Body struct {
		Following bool  `json:"following"`
		Followers int64 `json:"followers"`
	}
}

// followable checks that the thing exists and is public.
//
// A follow of something that is not there would be a row pointing at nothing,
// and a follow of a draft would be a way to learn that the draft exists — and
// to be told the moment it is published, ahead of everybody else.
func (a *API) followable(ctx context.Context, target models.FollowTarget, id string) error {
	if !target.Valid() {
		return huma.Error404NotFound("only a collective or a topic can be followed")
	}

	switch target {
	case models.FollowCollective:
		collective, err := a.store.Collective(ctx, id)
		if errors.Is(err, store.ErrCollectiveNotFound) || (err == nil && !collective.Status.Public()) {
			return huma.Error404NotFound("no such collective")
		}
		if err != nil {
			log.Error().Err(err).Msg("cannot read a collective to follow")
			return huma.Error500InternalServerError("cannot follow that")
		}
	case models.FollowTopic:
		topic, err := a.store.Topic(ctx, id)
		if errors.Is(err, store.ErrTopicNotFound) || (err == nil && !topic.Status.Public()) {
			return huma.Error404NotFound("no such topic")
		}
		if err != nil {
			log.Error().Err(err).Msg("cannot read a topic to follow")
			return huma.Error500InternalServerError("cannot follow that")
		}
	}
	return nil
}

func (a *API) follow(ctx context.Context, in *FollowInput) (*FollowOutput, error) {
	who, err := mustCaller(ctx)
	if err != nil {
		return nil, err
	}
	target := models.FollowTarget(in.Target)
	if err := a.followable(ctx, target, in.ID); err != nil {
		return nil, err
	}

	if err := a.store.Follow(ctx, who.Account.ID, target, in.ID); err != nil {
		log.Error().Err(err).Msg("cannot record a follow")
		return nil, huma.Error500InternalServerError("cannot follow that")
	}

	out := &FollowOutput{}
	out.Body.Followers, out.Body.Following = a.followState(ctx, target, in.ID)
	return out, nil
}

func (a *API) unfollow(ctx context.Context, in *FollowInput) (*FollowOutput, error) {
	who, err := mustCaller(ctx)
	if err != nil {
		return nil, err
	}
	target := models.FollowTarget(in.Target)
	if !target.Valid() {
		return nil, huma.Error404NotFound("only a collective or a topic can be followed")
	}

	// Not checked for existence: stopping following something that has since
	// been deleted or unpublished must work, or the reader is left with an
	// entry in their list they cannot remove.
	if err := a.store.Unfollow(ctx, who.Account.ID, target, in.ID); err != nil {
		log.Error().Err(err).Msg("cannot remove a follow")
		return nil, huma.Error500InternalServerError("cannot stop following that")
	}

	out := &FollowOutput{}
	out.Body.Followers, out.Body.Following = a.followState(ctx, target, in.ID)
	return out, nil
}

// FollowingOutput is everything one account follows.
type FollowingOutput struct {
	Body struct {
		Collectives []CollectiveItem `json:"collectives"`
		Topics      []TopicItem      `json:"topics"`
	}
}

func (a *API) listFollowing(ctx context.Context, _ *struct{}) (*FollowingOutput, error) {
	who, err := mustCaller(ctx)
	if err != nil {
		return nil, err
	}

	following, err := a.store.FollowingOf(ctx, who.Account.ID)
	if err != nil {
		log.Error().Err(err).Msg("cannot read what an account follows")
		return nil, huma.Error500InternalServerError("cannot read what you follow")
	}

	collectives, err := a.store.CollectivesByID(ctx, following.Collectives)
	if err != nil {
		log.Error().Err(err).Msg("cannot read followed collectives")
		return nil, huma.Error500InternalServerError("cannot read what you follow")
	}
	topics, err := a.store.TopicsByID(ctx, following.Topics)
	if err != nil {
		log.Error().Err(err).Msg("cannot read followed topics")
		return nil, huma.Error500InternalServerError("cannot read what you follow")
	}
	owners, err := a.collectivesOfTopics(ctx, topics)
	if err != nil {
		log.Error().Err(err).Msg("cannot read the collectives of followed topics")
	}

	out := &FollowingOutput{}
	out.Body.Collectives = []CollectiveItem{}
	out.Body.Topics = []TopicItem{}

	// In the order they were followed, newest first, which is the order the
	// store read them in. What has since been unpublished is left out rather
	// than shown as a dead link; the follow itself stays, so it comes back if
	// the thing does.
	for _, id := range following.Collectives {
		if collective, ok := collectives[id]; ok && collective.Status.Public() {
			item := toCollectiveItem(collective)
			item.Following = true
			out.Body.Collectives = append(out.Body.Collectives, item)
		}
	}
	for _, id := range following.Topics {
		if topic, ok := topics[id]; ok && topic.Status.Public() {
			item := toTopicItem(topic, owners[topic.CollectiveID])
			item.Following = true
			out.Body.Topics = append(out.Body.Topics, item)
		}
	}
	return out, nil
}

// ActionIDInput addresses an action.
type ActionIDInput struct {
	ID string `path:"id"`
}

// ParticipationOutput says where things stand after a change.
type ParticipationOutput struct {
	Body struct {
		Participating bool  `json:"participating"`
		Participants  int64 `json:"participants"`
	}
}

func (a *API) participate(ctx context.Context, in *ActionIDInput) (*ParticipationOutput, error) {
	who, err := mustCaller(ctx)
	if err != nil {
		return nil, err
	}

	action, err := a.store.Action(ctx, in.ID)
	if errors.Is(err, store.ErrActionNotFound) || (err == nil && !action.Status.Public()) {
		return nil, huma.Error404NotFound("no such action")
	}
	if err != nil {
		log.Error().Err(err).Msg("cannot read an action to attend")
		return nil, huma.Error500InternalServerError("cannot record that")
	}
	if action.Cancelled {
		// Said plainly rather than accepted and ignored: somebody pressing
		// "I'm coming" on a page they opened yesterday needs to learn that it
		// is off, and this is the moment they are looking.
		return nil, huma.Error409Conflict("this action has been cancelled")
	}

	if err := a.store.Participate(ctx, who.Account.ID, action.ID); err != nil {
		log.Error().Err(err).Msg("cannot record an intent to attend")
		return nil, huma.Error500InternalServerError("cannot record that")
	}
	return a.participation(ctx, action.ID, true), nil
}

func (a *API) withdraw(ctx context.Context, in *ActionIDInput) (*ParticipationOutput, error) {
	who, err := mustCaller(ctx)
	if err != nil {
		return nil, err
	}
	if err := a.store.Withdraw(ctx, who.Account.ID, in.ID); err != nil {
		log.Error().Err(err).Msg("cannot withdraw an intent to attend")
		return nil, huma.Error500InternalServerError("cannot record that")
	}
	return a.participation(ctx, in.ID, false), nil
}

func (a *API) participation(ctx context.Context, actionID string, participating bool) *ParticipationOutput {
	out := &ParticipationOutput{}
	out.Body.Participating = participating

	counts, err := a.store.ParticipantCounts(ctx, []string{actionID})
	if err != nil {
		log.Error().Err(err).Msg("cannot count the people coming to an action")
		return out
	}
	out.Body.Participants = counts[actionID]
	return out
}
