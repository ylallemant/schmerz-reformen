package store

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/ylallemant/schmerz-reformen/internal/models"
)

// Audit appends one entry to the log.
//
// There is no update and no delete beside it, in this file or any other: the
// log is append-only by having no other verb.
func (s *Store) Audit(ctx context.Context, entry models.AuditEntry) error {
	if entry.ID == "" {
		entry.ID = models.NewID()
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	return s.db.WithContext(ctx).Create(&entry).Error
}

// AuditQuery narrows a reading of the log.
type AuditQuery struct {
	// CollectiveIDs limits the log to the named collectives' content. Nil
	// means everything, which is an administrator's view; an empty non-nil
	// slice means nothing.
	CollectiveIDs []string

	Page
}

// ListAudit returns the log, newest first, with the number of entries matching
// the query.
func (s *Store) ListAudit(ctx context.Context, query AuditQuery) ([]models.AuditEntry, int64, error) {
	if query.CollectiveIDs != nil && len(query.CollectiveIDs) == 0 {
		return nil, 0, nil
	}
	page := query.Page.clamp(100, 500)

	db := s.db.WithContext(ctx).Model(&models.AuditEntry{})
	if query.CollectiveIDs != nil {
		db = db.Where("collective_id IN ?", query.CollectiveIDs)
	}

	var total int64
	if err := db.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var entries []models.AuditEntry
	err := db.Order("created_at desc").
		Limit(page.Limit).Offset(page.Offset).
		Find(&entries).Error
	return entries, total, err
}
