package backend

import (
	"context"
	"errors"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/models"
	"github.com/ylallemant/schmerz-reformen/internal/store"
)

// The organisations a collective counts among its members. Their routes are
// registered with the collective's own, in collectives.go.
//
// A membership is the collective's own business — which organisations it
// lists, in which order — and is changed by its editors directly. The
// organisation is not: it appears in every collective that lists it, so it is
// changed only by agreement (organisations.go).

// MemberFields is what an editor's form sends to add an organisation to a
// collective, or to move it in the list.
type MemberFields struct {
	OrganisationID string `json:"organisation_id,omitempty" doc:"the organisation to add; ignored when moving one already listed"`
	Position       int    `json:"position,omitempty" doc:"where it sits in the list; lower is earlier"`
}

// CreateMemberInput adds an organisation to a collective.
type CreateMemberInput struct {
	ID   string `path:"id"`
	Body MemberFields
}

// MemberOutput is one member organisation.
type MemberOutput struct {
	Body MemberItem
}

func (a *API) staffCreateMember(ctx context.Context, in *CreateMemberInput) (*MemberOutput, error) {
	who, collective, err := a.collectiveAdminFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}

	organisationID := strings.TrimSpace(in.Body.OrganisationID)
	if organisationID == "" {
		return nil, huma.Error422UnprocessableEntity("choose the organisation to add")
	}
	member := &models.CollectiveMember{
		CollectiveID:   collective.ID,
		OrganisationID: organisationID,
		Position:       in.Body.Position,
	}
	err = a.store.SaveMember(ctx, member)
	switch {
	case errors.Is(err, store.ErrOrganisationNotFound):
		return nil, huma.Error422UnprocessableEntity("there is no such organisation")
	case errors.Is(err, store.ErrAlreadyMember):
		return nil, huma.Error409Conflict("that organisation is already a member of this collective")
	case err != nil:
		log.Error().Err(err).Str("collective", collective.ID).Msg("cannot add a member")
		return nil, huma.Error500InternalServerError("cannot add the organisation")
	}

	saved, err := a.store.Member(ctx, member.ID)
	if err != nil {
		log.Error().Err(err).Str("member", member.ID).Msg("cannot read a member back")
		return nil, huma.Error500InternalServerError("cannot add the organisation")
	}
	a.audit(ctx, who, models.AuditCreate, "member", saved.ID, collective.ID, saved.Organisation.Name)
	return &MemberOutput{Body: toMemberItem(saved)}, nil
}

// memberFor loads a membership and checks the editor may manage the
// collective it belongs to.
func (a *API) memberFor(ctx context.Context, id string) (*staff, models.CollectiveMember, error) {
	member, err := a.store.Member(ctx, id)
	if errors.Is(err, store.ErrMemberNotFound) {
		// Still requires an editor: an unauthenticated probe must not learn
		// which identifiers exist.
		if _, err := mustStaff(ctx); err != nil {
			return nil, models.CollectiveMember{}, err
		}
		return nil, models.CollectiveMember{}, huma.Error404NotFound("no such organisation")
	}
	if err != nil {
		log.Error().Err(err).Str("member", id).Msg("cannot read a member")
		return nil, models.CollectiveMember{}, huma.Error500InternalServerError("cannot read the organisation")
	}

	who, _, err := a.collectiveAdminFor(ctx, member.CollectiveID)
	if err != nil {
		return nil, models.CollectiveMember{}, err
	}
	return who, member, nil
}

// SaveMemberInput moves an organisation in a collective's list.
type SaveMemberInput struct {
	ID   string `path:"id"`
	Body MemberFields
}

func (a *API) staffSaveMember(ctx context.Context, in *SaveMemberInput) (*MemberOutput, error) {
	who, member, err := a.memberFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if in.Body.Position < 1 {
		return nil, huma.Error422UnprocessableEntity("the position is a number from 1")
	}
	member.Position = in.Body.Position
	if err := a.store.SaveMember(ctx, &member); err != nil {
		log.Error().Err(err).Str("member", member.ID).Msg("cannot save a member")
		return nil, huma.Error500InternalServerError("cannot save the organisation")
	}

	a.audit(ctx, who, models.AuditUpdate, "member", member.ID, member.CollectiveID, member.Organisation.Name)
	return &MemberOutput{Body: toMemberItem(member)}, nil
}

// MemberIDInput addresses a membership.
type MemberIDInput struct {
	ID string `path:"id"`
}

func (a *API) staffDeleteMember(ctx context.Context, in *MemberIDInput) (*DoneOutput, error) {
	who, member, err := a.memberFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if err := a.store.DeleteMember(ctx, member.ID); err != nil && !errors.Is(err, store.ErrMemberNotFound) {
		log.Error().Err(err).Str("member", member.ID).Msg("cannot remove a member")
		return nil, huma.Error500InternalServerError("cannot remove the organisation")
	}
	// The organisation and its logo stay: it may be in other collectives,
	// and deleting it is a change of its own.

	a.audit(ctx, who, models.AuditDelete, "member", member.ID, member.CollectiveID, member.Organisation.Name)
	return done(), nil
}
