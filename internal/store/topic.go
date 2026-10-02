package store

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/ylallemant/schmerz-reformen/internal/geo"
	"github.com/ylallemant/schmerz-reformen/internal/models"
)

// ErrTopicNotFound is returned when no topic matches.
var ErrTopicNotFound = errors.New("topic not found")

// ErrUpdateNotFound is returned when no topic update matches.
var ErrUpdateNotFound = errors.New("update not found")

// stampPublication records the first moment something became public.
//
// Set once and never moved. A listing is ordered by it, and an editor fixing a
// typo in something published last month must not send it back to the top of
// everybody's page — nor announce it to its followers a second time, which is
// what the "was it already public?" answer is for.
func stampPublication(status models.PublishStatus, publishedAt **time.Time) (first bool) {
	if status != models.StatusPublished || *publishedAt != nil {
		return false
	}
	now := time.Now().UTC()
	*publishedAt = &now
	return true
}

// newestFirst orders by publication, falling back to the last edit for a
// draft that has none.
//
// COALESCE rather than two sort keys, because the engines disagree about where
// a NULL sorts: PostgreSQL puts it first in a descending order and SQLite puts
// it last, so "published_at desc, updated_at desc" would show an editor their
// drafts at the top in production and at the bottom in development.
const newestFirst = "COALESCE(published_at, updated_at) desc"

// SaveTopic creates or updates a topic, and reports whether this write is the
// one that made it public.
func (s *Store) SaveTopic(ctx context.Context, topic *models.Topic) (published bool, err error) {
	published = stampPublication(topic.Status, &topic.PublishedAt)
	return published, s.db.WithContext(ctx).Save(topic).Error
}

// Topic returns one topic.
func (s *Store) Topic(ctx context.Context, id string) (models.Topic, error) {
	var topic models.Topic
	err := s.db.WithContext(ctx).First(&topic, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.Topic{}, ErrTopicNotFound
	}
	return topic, err
}

// TopicQuery narrows a listing of topics.
type TopicQuery struct {
	CollectiveID string

	// Statuses limits the listing; empty means every status.
	Statuses []models.PublishStatus

	Kinds  []models.TopicKind
	Levels []models.Level

	// Bounds limits the listing to a map viewport. Nil means everywhere.
	Bounds *geo.Box

	Page
}

// ListTopics returns topics, most recently published first, with the number
// matching the query before the page was cut.
//
// # The total ignores the viewport
//
// A viewport narrows what is drawn and never what is counted. How many cuts
// there are and how widely they are spread is itself the argument this site
// exists to make: a reader looking at three pins around their own town should
// be told there are four hundred across the country, because the point of
// putting them on one map is that it is one policy.
func (s *Store) ListTopics(ctx context.Context, query TopicQuery) ([]models.Topic, int64, error) {
	page := query.Page.clamp(200, 1000)

	db := s.db.WithContext(ctx).Model(&models.Topic{})
	if query.CollectiveID != "" {
		db = db.Where("collective_id = ?", query.CollectiveID)
	}
	if len(query.Statuses) > 0 {
		db = db.Where("status IN ?", query.Statuses)
	}
	if len(query.Kinds) > 0 {
		db = db.Where("kind IN ?", query.Kinds)
	}
	if len(query.Levels) > 0 {
		db = db.Where("level IN ?", query.Levels)
	}

	var total int64
	if err := db.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	db, box := s.inViewport(db, query.Bounds)

	var candidates []models.Topic
	err := db.Order(newestFirst).
		Limit(page.Limit).Offset(page.Offset).
		Find(&candidates).Error
	if err != nil {
		return nil, 0, err
	}
	if box == nil {
		return candidates, total, nil
	}

	topics := make([]models.Topic, 0, len(candidates))
	for _, topic := range candidates {
		if box.Contains(topic.Location.Latitude, topic.Location.Longitude) {
			topics = append(topics, topic)
		}
	}
	return topics, total, nil
}

// TopicsByID returns the named topics, keyed by identifier.
func (s *Store) TopicsByID(ctx context.Context, ids []string) (map[string]models.Topic, error) {
	out := make(map[string]models.Topic, len(ids))
	if len(ids) == 0 {
		return out, nil
	}

	var topics []models.Topic
	if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&topics).Error; err != nil {
		return nil, err
	}
	for _, topic := range topics {
		out[topic.ID] = topic
	}
	return out, nil
}

// DeleteTopic removes a topic, its updates and the follows attached to it.
//
// Actions that were about it survive and lose the link: a demonstration is
// still happening on Saturday whether or not the page it pointed at exists.
func (s *Store) DeleteTopic(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Delete(&models.Topic{}, "id = ?", id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrTopicNotFound
		}

		if err := tx.Delete(&models.TopicUpdate{}, "topic_id = ?", id).Error; err != nil {
			return err
		}
		err := tx.Delete(&models.Follow{}, "target_type = ? AND target_id = ?",
			models.FollowTopic, id).Error
		if err != nil {
			return err
		}
		return tx.Model(&models.Action{}).Where("topic_id = ?", id).
			Update("topic_id", "").Error
	})
}

// SaveUpdate creates or updates a topic update, and reports whether this
// write is the one that made it public.
func (s *Store) SaveUpdate(ctx context.Context, update *models.TopicUpdate) (published bool, err error) {
	published = stampPublication(update.Status, &update.PublishedAt)
	return published, s.db.WithContext(ctx).Save(update).Error
}

// Update returns one topic update.
func (s *Store) Update(ctx context.Context, id string) (models.TopicUpdate, error) {
	var update models.TopicUpdate
	err := s.db.WithContext(ctx).First(&update, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.TopicUpdate{}, ErrUpdateNotFound
	}
	return update, err
}

// UpdateQuery narrows a listing of topic updates.
type UpdateQuery struct {
	TopicID      string
	CollectiveID string

	// Statuses limits the listing; empty means every status.
	Statuses []models.PublishStatus

	// Following, when set, limits the feed to what one reader follows: updates
	// on the topics they follow and from the collectives they follow. It is
	// what turns the public feed into a personal one.
	Following *Following

	Page
}

// Following is what one account follows, as two lists of identifiers.
type Following struct {
	Collectives []string
	Topics      []string
}

// Empty reports whether the reader follows nothing at all.
func (f Following) Empty() bool {
	return len(f.Collectives) == 0 && len(f.Topics) == 0
}

// ListUpdates returns updates, newest first, with the number matching the
// query.
//
// **Newest first, and by nothing else.** A feed ranked by what readers do with
// it is a feed that rewards whoever shouts loudest, and the collectives here
// are allies sharing one page: a tenants' group in a small town has to appear
// above a federal union's post from an hour earlier, because it is newer.
func (s *Store) ListUpdates(ctx context.Context, query UpdateQuery) ([]models.TopicUpdate, int64, error) {
	page := query.Page.clamp(30, 200)

	db := s.db.WithContext(ctx).Model(&models.TopicUpdate{})
	if query.TopicID != "" {
		db = db.Where("topic_id = ?", query.TopicID)
	}
	if query.CollectiveID != "" {
		db = db.Where("collective_id = ?", query.CollectiveID)
	}
	if len(query.Statuses) > 0 {
		db = db.Where("status IN ?", query.Statuses)
	}
	if query.Following != nil {
		if query.Following.Empty() {
			return nil, 0, nil
		}
		followed := s.db.Session(&gorm.Session{NewDB: true})
		switch {
		case len(query.Following.Topics) == 0:
			followed = followed.Where("collective_id IN ?", query.Following.Collectives)
		case len(query.Following.Collectives) == 0:
			followed = followed.Where("topic_id IN ?", query.Following.Topics)
		default:
			followed = followed.Where("collective_id IN ?", query.Following.Collectives).
				Or("topic_id IN ?", query.Following.Topics)
		}
		db = db.Where(followed)
	}

	var total int64
	if err := db.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var updates []models.TopicUpdate
	err := db.Order(newestFirst).
		Limit(page.Limit).Offset(page.Offset).
		Find(&updates).Error
	return updates, total, err
}

// DeleteUpdate removes a topic update.
func (s *Store) DeleteUpdate(ctx context.Context, id string) error {
	result := s.db.WithContext(ctx).Delete(&models.TopicUpdate{}, "id = ?", id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrUpdateNotFound
	}
	return nil
}

// TotalAmount sums the money at stake across every published topic that gave
// a figure, in whole euros.
//
// Published only, and under a published collective only: a draft is not yet a
// claim anybody is making in public, and adding it to a number on the front
// page would publish it.
func (s *Store) TotalAmount(ctx context.Context) (int64, error) {
	var total struct{ Sum int64 }
	err := s.db.WithContext(ctx).Model(&models.Topic{}).
		Select("COALESCE(SUM(amount), 0) AS sum").
		Where("status = ? AND collective_id IN (?)", models.StatusPublished,
			s.db.Model(&models.Collective{}).Select("id").
				Where("status = ?", models.StatusPublished)).
		Scan(&total).Error
	return total.Sum, err
}
