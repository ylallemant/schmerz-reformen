package store

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/ylallemant/schmerz-reformen/internal/models"
)

// ErrOrganisationNotFound is returned when no organisation matches.
var ErrOrganisationNotFound = errors.New("organisation not found")

// ErrParentNotFound means the parent a change names does not exist.
var ErrParentNotFound = errors.New("the parent organisation does not exist")

// ErrParentLoop means a parent would make an organisation part of itself.
var ErrParentLoop = errors.New("an organisation cannot be part of itself, directly or through another")

// ErrOrganisationInUse means an organisation is still in a collective, or is
// still the parent of another, and so cannot be deleted.
var ErrOrganisationInUse = errors.New("the organisation is still a member of a collective or the parent of another")

// maxParentDepth bounds the walk up a chain of parents. Real chains are three
// or four long — a local branch, a district, a state, a federation — so this
// is only a guard against a chain that is broken in a way nothing here can
// produce.
const maxParentDepth = 32

// Organisation returns one organisation.
func (s *Store) Organisation(ctx context.Context, id string) (models.Organisation, error) {
	return organisationIn(s.db.WithContext(ctx), id)
}

func organisationIn(tx *gorm.DB, id string) (models.Organisation, error) {
	var organisation models.Organisation
	err := tx.First(&organisation, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.Organisation{}, ErrOrganisationNotFound
	}
	return organisation, err
}

// OrganisationQuery narrows a listing of organisations.
type OrganisationQuery struct {
	// IDs limits the listing to these. Nil means no such limit; an empty
	// non-nil slice means none at all.
	IDs []string

	Page
}

// ListOrganisations returns organisations by name, with the number matching
// the query before the page was cut.
//
// There is no search here, on purpose: the console's picker filters a list
// it already holds, as the editor types, and an installation has hundreds of
// organisations rather than millions.
func (s *Store) ListOrganisations(ctx context.Context, query OrganisationQuery) ([]models.Organisation, int64, error) {
	if query.IDs != nil && len(query.IDs) == 0 {
		return nil, 0, nil
	}
	page := query.Page.clamp(1000, 5000)

	db := s.db.WithContext(ctx).Model(&models.Organisation{})
	if query.IDs != nil {
		db = db.Where("id IN ?", query.IDs)
	}

	var total int64
	if err := db.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var organisations []models.Organisation
	err := db.Order("name asc, id asc").Limit(page.Limit).Offset(page.Offset).
		Find(&organisations).Error
	return organisations, total, err
}

// OrganisationsByID returns the named organisations, keyed by identifier.
func (s *Store) OrganisationsByID(ctx context.Context, ids []string) (map[string]models.Organisation, error) {
	out := make(map[string]models.Organisation, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var organisations []models.Organisation
	if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&organisations).Error; err != nil {
		return nil, err
	}
	for _, organisation := range organisations {
		out[organisation.ID] = organisation
	}
	return out, nil
}

// OrganisationCollectives returns the collectives an organisation is a member
// of, by name — every status, because this is what the console shows before
// somebody proposes to change something that appears in all of them.
func (s *Store) OrganisationCollectives(ctx context.Context, id string) ([]models.Collective, error) {
	var collectiveIDs []string
	err := s.db.WithContext(ctx).Model(&models.CollectiveMember{}).
		Where("organisation_id = ?", id).Distinct().Pluck("collective_id", &collectiveIDs).Error
	if err != nil || len(collectiveIDs) == 0 {
		return nil, err
	}
	var collectives []models.Collective
	err = s.db.WithContext(ctx).Where("id IN ?", collectiveIDs).Order("name asc").
		Find(&collectives).Error
	return collectives, err
}

// organisationInUse reports whether an organisation is in a collective or is
// another's parent — either of which forbids deleting it.
//
// Refused rather than cascaded: deleting a union must not quietly take it out
// of five alliances' lists, nor orphan its districts. Whoever wants it gone
// takes it out of those first, where each collective's editors can see it
// happen.
func organisationInUse(tx *gorm.DB, id string) error {
	var members, children int64
	if err := tx.Model(&models.CollectiveMember{}).
		Where("organisation_id = ?", id).Count(&members).Error; err != nil {
		return err
	}
	if err := tx.Model(&models.Organisation{}).
		Where("parent_id = ?", id).Count(&children).Error; err != nil {
		return err
	}
	if members > 0 || children > 0 {
		return ErrOrganisationInUse
	}
	return nil
}

// checkParent refuses a parent that does not exist, or that would make the
// organisation part of itself.
//
// The walk goes up from the proposed parent. The organisation itself is never
// read, so this works the same for one that is only being proposed.
func checkParent(tx *gorm.DB, organisationID, parentID string) error {
	if parentID == "" {
		return nil
	}
	if parentID == organisationID {
		return ErrParentLoop
	}

	current := parentID
	for depth := 0; current != "" && depth < maxParentDepth; depth++ {
		ancestor, err := organisationIn(tx, current)
		if errors.Is(err, ErrOrganisationNotFound) {
			if depth == 0 {
				return ErrParentNotFound
			}
			// An ancestor further up is gone: the chain ends there, which
			// is not this change's problem to solve.
			return nil
		}
		if err != nil {
			return err
		}
		if ancestor.ParentID == organisationID {
			return ErrParentLoop
		}
		current = ancestor.ParentID
	}
	return nil
}
