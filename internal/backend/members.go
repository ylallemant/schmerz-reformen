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

// The organisations inside a collective. Their routes are registered with the
// collective's own, in collectives.go: a member has no life outside one.

// MemberFields is what an editor's form sends for a member organisation.
type MemberFields struct {
	Name     string `json:"name"`
	Kind     string `json:"kind,omitempty" doc:"union, party, association, initiative or other"`
	Website  string `json:"website,omitempty"`
	Position int    `json:"position,omitempty" doc:"where it sits in the list; lower is earlier"`
}

func applyMemberFields(member *models.CollectiveMember, fields MemberFields) error {
	name, err := cleanLine("the name", fields.Name, maxNameRunes)
	if err != nil {
		return err
	}
	if name == "" {
		return huma.Error422UnprocessableEntity("a member organisation needs a name")
	}
	website, err := cleanURL("the website", fields.Website)
	if err != nil {
		return err
	}

	kind := models.MemberKind(strings.TrimSpace(fields.Kind))
	if kind == "" {
		kind = models.MemberOther
	}
	if !kind.Valid() {
		return huma.Error422UnprocessableEntity("unknown kind of organisation")
	}

	member.Name = name
	member.Kind = kind
	member.Website = website
	if fields.Position != 0 {
		member.Position = fields.Position
	}
	return nil
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
	who, collective, err := a.collectiveFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}

	member := &models.CollectiveMember{CollectiveID: collective.ID}
	if err := applyMemberFields(member, in.Body); err != nil {
		return nil, err
	}
	if err := a.store.SaveMember(ctx, member); err != nil {
		log.Error().Err(err).Str("collective", collective.ID).Msg("cannot add a member")
		return nil, huma.Error500InternalServerError("cannot add the organisation")
	}

	a.audit(ctx, who, models.AuditCreate, "member", member.ID, collective.ID, member.Name)
	return &MemberOutput{Body: toMemberItem(*member)}, nil
}

// memberFor loads a member organisation and checks the editor may manage the
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

	who, _, err := a.collectiveFor(ctx, member.CollectiveID)
	if err != nil {
		return nil, models.CollectiveMember{}, huma.Error404NotFound("no such organisation")
	}
	return who, member, nil
}

// SaveMemberInput changes a member organisation.
type SaveMemberInput struct {
	ID   string `path:"id"`
	Body MemberFields
}

func (a *API) staffSaveMember(ctx context.Context, in *SaveMemberInput) (*MemberOutput, error) {
	who, member, err := a.memberFor(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if err := applyMemberFields(&member, in.Body); err != nil {
		return nil, err
	}
	if err := a.store.SaveMember(ctx, &member); err != nil {
		log.Error().Err(err).Str("member", member.ID).Msg("cannot save a member")
		return nil, huma.Error500InternalServerError("cannot save the organisation")
	}

	a.audit(ctx, who, models.AuditUpdate, "member", member.ID, member.CollectiveID, member.Name)
	return &MemberOutput{Body: toMemberItem(member)}, nil
}

// MemberIDInput addresses a member organisation.
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
	// Its logo was only ever its own.
	a.dropMedia(ctx, member.LogoID)

	a.audit(ctx, who, models.AuditDelete, "member", member.ID, member.CollectiveID, member.Name)
	return done(), nil
}
