package store

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/ylallemant/schmerz-reformen/internal/geo"
	"github.com/ylallemant/schmerz-reformen/internal/models"
)

// ErrActionNotFound is returned when no action matches.
var ErrActionNotFound = errors.New("action not found")

// SaveAction creates or updates an action, and reports whether this write is
// the one that made it public.
func (s *Store) SaveAction(ctx context.Context, action *models.Action) (published bool, err error) {
	published = stampPublication(action.Status, &action.PublishedAt)
	return published, s.db.WithContext(ctx).Save(action).Error
}

// Action returns one action.
func (s *Store) Action(ctx context.Context, id string) (models.Action, error) {
	var action models.Action
	err := s.db.WithContext(ctx).First(&action, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.Action{}, ErrActionNotFound
	}
	return action, err
}

// ActionQuery narrows a listing of actions.
type ActionQuery struct {
	CollectiveID string
	TopicID      string

	// IDs limits the listing to the named actions — the ones a reader said
	// they are coming to. Nil means no such limit; an empty non-nil slice
	// means none at all.
	IDs []string

	// Statuses limits the listing; empty means every status.
	Statuses []models.PublishStatus

	Kinds []models.ActionKind

	// From and To bound the start time: From inclusive, To exclusive. A zero
	// value leaves that side open.
	From time.Time
	To   time.Time

	// Bounds limits the listing to a map viewport. Nil means everywhere.
	Bounds *geo.Box

	// Descending lists the latest first. The calendar reads forwards from
	// today; the console reads backwards from the far future.
	Descending bool

	Page
}

// ListActions returns actions in the order they happen, with the number
// matching the query before the viewport and the page narrowed it.
//
// **In the order they happen, and by nothing else.** A calendar sorted by how
// many people said they were coming would put the big unions' demonstrations
// above a neighbourhood's meeting on the same evening, on a site whose point
// is that they are the same fight.
func (s *Store) ListActions(ctx context.Context, query ActionQuery) ([]models.Action, int64, error) {
	if query.IDs != nil && len(query.IDs) == 0 {
		return nil, 0, nil
	}
	page := query.Page.clamp(200, 1000)

	db := s.db.WithContext(ctx).Model(&models.Action{})
	if query.CollectiveID != "" {
		db = db.Where("collective_id = ?", query.CollectiveID)
	}
	if query.TopicID != "" {
		db = db.Where("topic_id = ?", query.TopicID)
	}
	if query.IDs != nil {
		db = db.Where("id IN ?", query.IDs)
	}
	if len(query.Statuses) > 0 {
		db = db.Where("status IN ?", query.Statuses)
	}
	if len(query.Kinds) > 0 {
		db = db.Where("kind IN ?", query.Kinds)
	}
	if !query.From.IsZero() {
		db = db.Where("starts_at >= ?", query.From.UTC())
	}
	if !query.To.IsZero() {
		db = db.Where("starts_at < ?", query.To.UTC())
	}

	var total int64
	if err := db.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	db, box := s.inViewport(db, query.Bounds)

	order := "starts_at asc"
	if query.Descending {
		order = "starts_at desc"
	}

	var candidates []models.Action
	err := db.Order(order).Limit(page.Limit).Offset(page.Offset).Find(&candidates).Error
	if err != nil {
		return nil, 0, err
	}
	if box == nil {
		return candidates, total, nil
	}

	actions := make([]models.Action, 0, len(candidates))
	for _, action := range candidates {
		if box.Contains(action.Location.Latitude, action.Location.Longitude) {
			actions = append(actions, action)
		}
	}
	return actions, total, nil
}

// DeleteAction removes an action and the intents attached to it.
//
// This is for something that should never have been announced — a duplicate, a
// mistake. An action that was announced and is not happening is **cancelled**,
// not deleted: see models.Action.Cancelled.
func (s *Store) DeleteAction(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Delete(&models.Action{}, "id = ?", id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrActionNotFound
		}
		return tx.Delete(&models.Participation{}, "action_id = ?", id).Error
	})
}
