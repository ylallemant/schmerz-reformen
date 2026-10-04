package store

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/ylallemant/schmerz-reformen/internal/geo"
	"github.com/ylallemant/schmerz-reformen/internal/models"
)

// ErrCollectiveNotFound is returned when no collective matches.
var ErrCollectiveNotFound = errors.New("collective not found")

// ErrMemberNotFound is returned when no member organisation matches.
var ErrMemberNotFound = errors.New("member not found")

// ErrAlreadyMember means the organisation is already in the collective.
var ErrAlreadyMember = errors.New("that organisation is already a member of this collective")

// ErrSlugTaken means another collective already answers at that address.
var ErrSlugTaken = errors.New("that address is already taken")

// CreateCollective records a new collective.
//
// The slug is checked inside the write rather than trusted from a form:
// between the keystroke and the submission somebody else may have taken it,
// and the unique index would otherwise turn that race into a bare constraint
// error nobody can show an editor.
func (s *Store) CreateCollective(ctx context.Context, collective *models.Collective) error {
	collective.Slug = models.Slugify(collective.Slug)
	if collective.Slug == "" {
		return ErrSlugTaken
	}
	// Its groups are named from its slug now, once — the caller names them
	// with the provisioned application's prefix; a caller that did not gets
	// the default one.
	if collective.AdminGroup == "" || collective.AuthorGroup == "" {
		collective.AdminGroup, collective.AuthorGroup = models.CollectiveGroups(models.DefaultAppName, collective.Slug)
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var taken int64
		err := tx.Model(&models.Collective{}).
			Where("slug = ?", collective.Slug).Count(&taken).Error
		if err != nil {
			return err
		}
		if taken > 0 {
			return ErrSlugTaken
		}
		return tx.Omit("Members").Create(collective).Error
	})
}

// SaveCollective writes a changed collective.
//
// Members are omitted on purpose: they are rows of their own with endpoints of
// their own, and a profile edit that carried a stale copy of the list would
// quietly undo whatever somebody else had just changed in it.
func (s *Store) SaveCollective(ctx context.Context, collective *models.Collective) error {
	collective.Slug = models.Slugify(collective.Slug)
	if collective.Slug == "" {
		return ErrSlugTaken
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var taken int64
		err := tx.Model(&models.Collective{}).
			Where("slug = ? AND id <> ?", collective.Slug, collective.ID).
			Count(&taken).Error
		if err != nil {
			return err
		}
		if taken > 0 {
			return ErrSlugTaken
		}
		// The groups were fixed when it was created; a changed slug does not
		// rename them, or the people in them would lose their roles.
		return tx.Omit("Members", "AdminGroup", "AuthorGroup").Save(collective).Error
	})
}

// Collective returns one collective with its member organisations, in the
// order the collective put them.
func (s *Store) Collective(ctx context.Context, id string) (models.Collective, error) {
	return s.collectiveWhere(ctx, "id = ?", id)
}

// CollectiveBySlug returns the collective answering at an address.
func (s *Store) CollectiveBySlug(ctx context.Context, slug string) (models.Collective, error) {
	return s.collectiveWhere(ctx, "slug = ?", models.Slugify(slug))
}

func (s *Store) collectiveWhere(ctx context.Context, query string, arg any) (models.Collective, error) {
	var collective models.Collective
	err := s.db.WithContext(ctx).
		Preload("Members", func(db *gorm.DB) *gorm.DB {
			return db.Order("position asc, created_at asc")
		}).
		Preload("Members.Organisation").
		Where(query, arg).First(&collective).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.Collective{}, ErrCollectiveNotFound
	}
	return collective, err
}

// CollectiveQuery narrows a listing of collectives.
type CollectiveQuery struct {
	// Statuses limits the listing; empty means every status.
	Statuses []models.PublishStatus

	// Groups limits the listing to collectives one of these identity-provider
	// groups administers or writes for. Nil means no such limit; an empty
	// non-nil slice means none at all — an editor in no group has nothing,
	// and reading "no groups" as "no filter" would hand them everything.
	Groups []string

	// Bounds limits the listing to a map viewport. Nil means everywhere.
	Bounds *geo.Box

	Page
}

// ListCollectives returns collectives by name, with the number matching the
// query before the page was cut.
func (s *Store) ListCollectives(ctx context.Context, query CollectiveQuery) ([]models.Collective, int64, error) {
	if query.Groups != nil && len(query.Groups) == 0 {
		return nil, 0, nil
	}
	page := query.Page.clamp(200, 500)

	db := s.db.WithContext(ctx).Model(&models.Collective{})
	if len(query.Statuses) > 0 {
		db = db.Where("status IN ?", query.Statuses)
	}
	if query.Groups != nil {
		db = db.Where("admin_group IN ? OR author_group IN ?", query.Groups, query.Groups)
	}

	// Counted before the viewport narrows it: see ListTopics for why a map
	// narrows what is drawn and never what is counted.
	var total int64
	if err := db.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	db, box := s.inViewport(db, query.Bounds)

	var candidates []models.Collective
	err := db.Order("name asc").Limit(page.Limit).Offset(page.Offset).
		Find(&candidates).Error
	if err != nil {
		return nil, 0, err
	}
	if box == nil {
		return candidates, total, nil
	}

	collectives := make([]models.Collective, 0, len(candidates))
	for _, collective := range candidates {
		if box.Contains(collective.Location.Latitude, collective.Location.Longitude) {
			collectives = append(collectives, collective)
		}
	}
	return collectives, total, nil
}

// CollectivesByID returns the named collectives, keyed by identifier. A
// listing of topics or actions uses it to name whose each one is without a
// query per row.
func (s *Store) CollectivesByID(ctx context.Context, ids []string) (map[string]models.Collective, error) {
	out := make(map[string]models.Collective, len(ids))
	if len(ids) == 0 {
		return out, nil
	}

	var collectives []models.Collective
	err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&collectives).Error
	if err != nil {
		return nil, err
	}
	for _, collective := range collectives {
		out[collective.ID] = collective
	}
	return out, nil
}

// DeleteCollective removes a collective and everything it published.
//
// It returns the storage keys of the images that were its own, so the caller
// can remove the bytes: the rows are gone when this returns, and a blob whose
// row is gone is invisible and never reclaimed.
//
// Everything hanging from it goes in the same transaction — topics, updates,
// actions, and the follows and intents readers attached to them. A follow
// pointing at nothing is a row about a reader that serves no reader.
func (s *Store) DeleteCollective(ctx context.Context, id string) ([]string, error) {
	var keys []string

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var collective models.Collective
		if err := tx.First(&collective, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrCollectiveNotFound
			}
			return err
		}

		var media []models.Media
		if err := tx.Where("collective_id = ?", id).Find(&media).Error; err != nil {
			return err
		}
		for _, item := range media {
			keys = append(keys, item.Key)
		}

		var topicIDs, actionIDs []string
		if err := tx.Model(&models.Topic{}).Where("collective_id = ?", id).
			Pluck("id", &topicIDs).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.Action{}).Where("collective_id = ?", id).
			Pluck("id", &actionIDs).Error; err != nil {
			return err
		}

		if len(topicIDs) > 0 {
			err := tx.Delete(&models.Follow{}, "target_type = ? AND target_id IN ?",
				models.FollowTopic, topicIDs).Error
			if err != nil {
				return err
			}
		}
		if len(actionIDs) > 0 {
			err := tx.Delete(&models.Participation{}, "action_id IN ?", actionIDs).Error
			if err != nil {
				return err
			}
		}
		err := tx.Delete(&models.Follow{}, "target_type = ? AND target_id = ?",
			models.FollowCollective, id).Error
		if err != nil {
			return err
		}

		for _, model := range []any{
			&models.TopicUpdate{}, &models.Topic{}, &models.Action{},
			&models.CollectiveMember{}, &models.Media{},
		} {
			if err := tx.Delete(model, "collective_id = ?", id).Error; err != nil {
				return err
			}
		}
		return tx.Delete(&models.Collective{}, "id = ?", id).Error
	})
	return keys, err
}

// Member returns one membership, with its organisation.
func (s *Store) Member(ctx context.Context, id string) (models.CollectiveMember, error) {
	var member models.CollectiveMember
	err := s.db.WithContext(ctx).Preload("Organisation").First(&member, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.CollectiveMember{}, ErrMemberNotFound
	}
	return member, err
}

// SaveMember adds an organisation to a collective, or moves it in the list.
//
// A new member goes to the end of the list. Where it belongs after that is the
// collective's decision — see CollectiveMember.Position. The organisation is
// never written through a membership: it is changed only by agreement.
func (s *Store) SaveMember(ctx context.Context, member *models.CollectiveMember) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if member.ID == "" {
			if _, err := organisationIn(tx, member.OrganisationID); err != nil {
				return err
			}
			var already int64
			err := tx.Model(&models.CollectiveMember{}).
				Where("collective_id = ? AND organisation_id = ?", member.CollectiveID, member.OrganisationID).
				Count(&already).Error
			if err != nil {
				return err
			}
			if already > 0 {
				return ErrAlreadyMember
			}
		}

		if member.ID == "" && member.Position == 0 {
			var last models.CollectiveMember
			err := tx.Where("collective_id = ?", member.CollectiveID).
				Order("position desc").First(&last).Error
			switch {
			case err == nil:
				member.Position = last.Position + 1
			case errors.Is(err, gorm.ErrRecordNotFound):
				member.Position = 1
			default:
				return err
			}
		}
		return tx.Omit("Organisation").Save(member).Error
	})
}

// DeleteMember takes an organisation out of a collective. The organisation
// itself stays: it may be in others, and deleting it is a change of its own.
func (s *Store) DeleteMember(ctx context.Context, id string) error {
	result := s.db.WithContext(ctx).Delete(&models.CollectiveMember{}, "id = ?", id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrMemberNotFound
	}
	return nil
}
