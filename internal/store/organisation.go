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
		// An organisation not created yet has no identifier and cannot be
		// anybody's ancestor; compared as one, the empty parent at the top
		// of every chain would read as a loop.
		if organisationID != "" && ancestor.ParentID == organisationID {
			return ErrParentLoop
		}
		current = ancestor.ParentID
	}
	return nil
}

// ErrOrganisationSlugTaken means another organisation already has the slug.
var ErrOrganisationSlugTaken = errors.New("another organisation already has that name in its groups")

// CreateOrganisation records a new organisation, with a slug free among the
// others: a name another organisation already took gets -2, -3… so two
// branches called the same can both exist, each with groups of its own.
func (s *Store) CreateOrganisation(ctx context.Context, organisation *models.Organisation, app string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		name := organisation.Slug
		if models.Slugify(name) == "" {
			name = organisation.Name
		}
		slug, err := freeOrganisationSlug(tx, name)
		if err != nil {
			return err
		}
		organisation.Slug = slug
		organisation.AdminGroup, organisation.MemberGroup = models.OrganisationGroups(app, slug)
		return tx.Create(organisation).Error
	})
}

// SaveOrganisation writes a changed organisation. The slug and the groups
// are not the form's to change: they were fixed when it was created.
func (s *Store) SaveOrganisation(ctx context.Context, organisation *models.Organisation) error {
	return s.db.WithContext(ctx).Model(organisation).
		Omit("Slug", "AdminGroup", "MemberGroup", "CreatedAt").
		Save(organisation).Error
}

// DeleteOrganisation removes one that nothing uses.
//
// Refused while it is in a collective or another's parent, never cascaded:
// deleting a union must not quietly take it out of five alliances' lists.
func (s *Store) DeleteOrganisation(ctx context.Context, id string) (models.Organisation, error) {
	var deleted models.Organisation
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		organisation, err := organisationIn(tx, id)
		if err != nil {
			return err
		}
		if err := organisationInUse(tx, id); err != nil {
			return err
		}
		deleted = organisation
		return tx.Delete(&models.Organisation{}, "id = ?", id).Error
	})
	return deleted, err
}

// CheckParent refuses a parent that does not exist, or that would make the
// organisation part of itself.
func (s *Store) CheckParent(ctx context.Context, organisationID, parentID string) error {
	return checkParent(s.db.WithContext(ctx), organisationID, parentID)
}

// OrganisationsByGroups returns the organisations one of these groups
// administers or counts as members.
func (s *Store) OrganisationsByGroups(ctx context.Context, groups []string) ([]models.Organisation, error) {
	if len(groups) == 0 {
		return nil, nil
	}
	var organisations []models.Organisation
	err := s.db.WithContext(ctx).
		Where("admin_group IN ? OR member_group IN ?", groups, groups).
		Order("name asc").Find(&organisations).Error
	return organisations, err
}
