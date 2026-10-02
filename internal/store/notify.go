package store

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm/clause"

	"github.com/ylallemant/schmerz-reformen/internal/models"
)

// ErrSubscriptionNotFound means no such push endpoint is registered here.
var ErrSubscriptionNotFound = errors.New("no such push subscription")

// Subscribe records that a browser has agreed to be told things.
//
// Upsert on the endpoint, because a browser that re-subscribes is handed the
// same endpoint back: two rows for it would send every notification twice to
// one screen, and the person would have no way to tell which of the two to
// remove.
//
// The keys are overwritten rather than kept, because a re-subscription can
// carry new ones — a browser that lost its permission and was granted it again
// generates a fresh pair, and the old ones then encrypt to nothing.
func (s *Store) Subscribe(ctx context.Context, subscription *models.PushSubscription) error {
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "endpoint"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"account_id", "p256dh", "auth", "label", "updated_at",
		}),
	}).Create(subscription).Error
}

// ListSubscriptions is which of somebody's devices can be reached.
func (s *Store) ListSubscriptions(ctx context.Context, accountID string) ([]models.PushSubscription, error) {
	var subscriptions []models.PushSubscription
	err := s.db.WithContext(ctx).
		Where("account_id = ?", accountID).
		Order("created_at desc").
		Find(&subscriptions).Error
	return subscriptions, err
}

// Unsubscribe removes one device from somebody's own list.
func (s *Store) Unsubscribe(ctx context.Context, accountID, subscriptionID string) error {
	result := s.db.WithContext(ctx).
		Delete(&models.PushSubscription{}, "id = ? AND account_id = ?", subscriptionID, accountID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrSubscriptionNotFound
	}
	return nil
}

// UnsubscribeEndpoint deletes a subscription the push service says is gone.
//
// This is the whole of the delivery-failure story, and it is deliberately not
// a retry ladder. A mailbox can be temporarily full, so retrying one makes
// sense; a push endpoint answering 404 or 410 is not there any more and will
// not be. There is no backoff and no second phase.
func (s *Store) UnsubscribeEndpoint(ctx context.Context, endpoint string) error {
	return s.db.WithContext(ctx).
		Delete(&models.PushSubscription{}, "endpoint = ?", endpoint).Error
}

// RecordPushSent marks a subscription as having worked.
func (s *Store) RecordPushSent(ctx context.Context, endpoint string) error {
	now := time.Now()
	return s.db.WithContext(ctx).Model(&models.PushSubscription{}).
		Where("endpoint = ?", endpoint).
		Update("last_used_at", now).Error
}

// Notify writes one thing worth telling somebody.
//
// The row comes first and the push second, always. A notification that existed
// only as a push would be lost to a refused permission, an iPhone that has not
// installed the site, or an endpoint that died between one week and the next —
// and the person would simply never find out.
func (s *Store) Notify(ctx context.Context, notification *models.Notification) error {
	return s.db.WithContext(ctx).Create(notification).Error
}

// NotifyMany writes the same thing to several people at once.
func (s *Store) NotifyMany(ctx context.Context, notifications []models.Notification) error {
	if len(notifications) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Create(&notifications).Error
}

// ListNotifications is somebody's own list, newest first.
func (s *Store) ListNotifications(ctx context.Context, accountID string, limit, offset int) ([]models.Notification, int64, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	var total int64
	err := s.db.WithContext(ctx).Model(&models.Notification{}).
		Where("account_id = ?", accountID).Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	var notifications []models.Notification
	err = s.db.WithContext(ctx).
		Where("account_id = ?", accountID).
		Order("created_at desc").
		Limit(limit).Offset(offset).
		Find(&notifications).Error
	return notifications, total, err
}

// CountUnread is the number on the bell.
func (s *Store) CountUnread(ctx context.Context, accountID string) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&models.Notification{}).
		Where("account_id = ? AND read_at IS NULL", accountID).
		Count(&count).Error
	return count, err
}

// MarkNotificationRead marks one item read.
func (s *Store) MarkNotificationRead(ctx context.Context, accountID, notificationID string) error {
	now := time.Now()
	result := s.db.WithContext(ctx).Model(&models.Notification{}).
		Where("id = ? AND account_id = ? AND read_at IS NULL", notificationID, accountID).
		Updates(map[string]any{"read_at": now, "updated_at": now})
	return result.Error
}

// MarkAllNotificationsRead clears the bell.
func (s *Store) MarkAllNotificationsRead(ctx context.Context, accountID string) error {
	now := time.Now()
	return s.db.WithContext(ctx).Model(&models.Notification{}).
		Where("account_id = ? AND read_at IS NULL", accountID).
		Updates(map[string]any{"read_at": now, "updated_at": now}).Error
}

// DeleteNotification removes one item from somebody's own list.
func (s *Store) DeleteNotification(ctx context.Context, accountID, notificationID string) error {
	return s.db.WithContext(ctx).
		Delete(&models.Notification{}, "id = ? AND account_id = ?", notificationID, accountID).Error
}

// SubscriptionsFor is every device belonging to the named accounts.
func (s *Store) SubscriptionsFor(ctx context.Context, accountIDs []string) ([]models.PushSubscription, error) {
	if len(accountIDs) == 0 {
		return nil, nil
	}
	var subscriptions []models.PushSubscription
	err := s.db.WithContext(ctx).Where("account_id IN ?", accountIDs).Find(&subscriptions).Error
	return subscriptions, err
}
