package store

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/ylallemant/schmerz-reformen/internal/models"
)

// ErrMediaNotFound is returned when no uploaded image matches.
var ErrMediaNotFound = errors.New("media not found")

// CreateMedia records an uploaded image. The bytes are the caller's to write
// to storage; this is the row that says they exist and whose they are.
func (s *Store) CreateMedia(ctx context.Context, media *models.Media) error {
	return s.db.WithContext(ctx).Create(media).Error
}

// Media returns the record of one uploaded image.
func (s *Store) Media(ctx context.Context, id string) (models.Media, error) {
	var media models.Media
	err := s.db.WithContext(ctx).First(&media, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.Media{}, ErrMediaNotFound
	}
	return media, err
}

// DeleteMedia removes the record and returns it, so the caller knows which key
// to remove from storage.
func (s *Store) DeleteMedia(ctx context.Context, id string) (models.Media, error) {
	media, err := s.Media(ctx, id)
	if err != nil {
		return models.Media{}, err
	}
	return media, s.db.WithContext(ctx).Delete(&models.Media{}, "id = ?", id).Error
}
