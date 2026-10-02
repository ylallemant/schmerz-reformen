package store

import (
	"context"

	"gorm.io/gorm/clause"

	"github.com/ylallemant/schmerz-reformen/internal/models"
)

// Follow records that an account wants to be told about a collective or a
// topic.
//
// Following twice is following once: the unique index makes the second write a
// no-op rather than an error, because a page left open in another tab offering
// "follow" for something already followed is not a mistake anybody made.
func (s *Store) Follow(ctx context.Context, accountID string, target models.FollowTarget, targetID string) error {
	return s.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&models.Follow{
			AccountID: accountID, TargetType: target, TargetID: targetID,
		}).Error
}

// Unfollow removes it. Removing what is not there is success, for the same
// reason following twice is.
func (s *Store) Unfollow(ctx context.Context, accountID string, target models.FollowTarget, targetID string) error {
	return s.db.WithContext(ctx).Delete(&models.Follow{},
		"account_id = ? AND target_type = ? AND target_id = ?",
		accountID, target, targetID).Error
}

// Follows reports whether an account follows something.
func (s *Store) Follows(ctx context.Context, accountID string, target models.FollowTarget, targetID string) (bool, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&models.Follow{}).
		Where("account_id = ? AND target_type = ? AND target_id = ?",
			accountID, target, targetID).
		Count(&count).Error
	return count > 0, err
}

// FollowingOf returns everything an account follows.
func (s *Store) FollowingOf(ctx context.Context, accountID string) (Following, error) {
	var follows []models.Follow
	err := s.db.WithContext(ctx).Where("account_id = ?", accountID).
		Order("created_at desc").Find(&follows).Error
	if err != nil {
		return Following{}, err
	}

	var following Following
	for _, follow := range follows {
		switch follow.TargetType {
		case models.FollowCollective:
			following.Collectives = append(following.Collectives, follow.TargetID)
		case models.FollowTopic:
			following.Topics = append(following.Topics, follow.TargetID)
		}
	}
	return following, nil
}

// CountFollowers returns how many accounts follow something.
//
// A count of accounts, not of people: an account costs a fingerprint and a
// second to make. It is shown as an indication and nothing is ever ranked,
// ordered or decided by it.
func (s *Store) CountFollowers(ctx context.Context, target models.FollowTarget, targetID string) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&models.Follow{}).
		Where("target_type = ? AND target_id = ?", target, targetID).
		Count(&count).Error
	return count, err
}

// FollowersOf returns the accounts to tell when something is published: those
// following the collective, and those following the topic it is about.
//
// One list with each account once. Somebody who follows both the alliance and
// the topic asked twice to be told and should be told once.
func (s *Store) FollowersOf(ctx context.Context, collectiveID, topicID string) ([]string, error) {
	db := s.db.WithContext(ctx).Model(&models.Follow{}).
		Where("target_type = ? AND target_id = ?", models.FollowCollective, collectiveID)
	if topicID != "" {
		db = db.Or("target_type = ? AND target_id = ?", models.FollowTopic, topicID)
	}

	var accounts []string
	err := db.Distinct().Pluck("account_id", &accounts).Error
	return accounts, err
}

// Participate records that an account intends to come to an action. Saying so
// twice is saying so once.
func (s *Store) Participate(ctx context.Context, accountID, actionID string) error {
	return s.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&models.Participation{AccountID: accountID, ActionID: actionID}).Error
}

// Withdraw removes the intent.
func (s *Store) Withdraw(ctx context.Context, accountID, actionID string) error {
	return s.db.WithContext(ctx).Delete(&models.Participation{},
		"account_id = ? AND action_id = ?", accountID, actionID).Error
}

// ParticipationsOf returns the actions an account said it is coming to.
func (s *Store) ParticipationsOf(ctx context.Context, accountID string) ([]string, error) {
	var actions []string
	err := s.db.WithContext(ctx).Model(&models.Participation{}).
		Where("account_id = ?", accountID).
		Pluck("action_id", &actions).Error
	return actions, err
}

// ParticipantsOf returns the accounts that said they are coming to an action,
// for telling them when it moves or is called off.
func (s *Store) ParticipantsOf(ctx context.Context, actionID string) ([]string, error) {
	var accounts []string
	err := s.db.WithContext(ctx).Model(&models.Participation{}).
		Where("action_id = ?", actionID).
		Pluck("account_id", &accounts).Error
	return accounts, err
}

// ParticipantCounts returns how many accounts said they are coming to each of
// the named actions. An action nobody answered for is simply absent, which a
// map lookup reads as zero.
func (s *Store) ParticipantCounts(ctx context.Context, actionIDs []string) (map[string]int64, error) {
	counts := make(map[string]int64, len(actionIDs))
	if len(actionIDs) == 0 {
		return counts, nil
	}

	var rows []struct {
		ActionID string
		Count    int64
	}
	err := s.db.WithContext(ctx).Model(&models.Participation{}).
		Select("action_id, COUNT(*) AS count").
		Where("action_id IN ?", actionIDs).
		Group("action_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		counts[row.ActionID] = row.Count
	}
	return counts, nil
}
