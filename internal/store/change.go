package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/ylallemant/schmerz-reformen/internal/models"
)

// ErrChangeNotFound is returned when no change matches.
var ErrChangeNotFound = errors.New("change not found")

// ErrChangeClosed means the change is no longer pending.
var ErrChangeClosed = errors.New("the change has already been decided")

// ErrOwnChange means somebody tried to vote on a change they proposed.
var ErrOwnChange = errors.New("the author of a change cannot vote on it")

// ErrAlreadyVoted means this editor has voted on the change before.
var ErrAlreadyVoted = errors.New("you have already voted on this change")

// ErrNotAuthor means somebody other than its author tried to withdraw a change.
var ErrNotAuthor = errors.New("only the author of a change can withdraw it")

// ErrNothingChanged means a proposed update is the same as what is there.
var ErrNothingChanged = errors.New("the change would change nothing")

// ErrChangeConflict means another pending change already touches the same
// thing.
var ErrChangeConflict = errors.New("another change to the same organisation is already waiting for approval")

// ProposeChange records a proposed creation, update or deletion. Nothing about
// the organisation changes until the change collects its approvals.
//
// What it would do is checked now, so nobody is asked to approve a change that
// could never be applied — and checked again when it is applied, because a
// parent that existed this morning may not by the time the third vote comes in.
//
// For an update, the change's After holds the whole form and Fields is worked
// out here, from what differs: a form posts every field it has, and a change
// that "set" the website to what it already was would block every other
// change to the website while it waited.
func (s *Store) ProposeChange(ctx context.Context, change *models.OrganisationChange) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		change.Status = models.ChangePending
		change.Votes = nil

		switch change.Kind {
		case models.ChangeCreate:
			if change.OrganisationID == "" {
				change.OrganisationID = models.NewID()
			}
			change.Before = models.OrganisationValues{}
			change.Fields = strings.Join(models.ChangeFields, ",")
			if err := checkParent(tx, change.OrganisationID, change.After.ParentID); err != nil {
				return err
			}

		case models.ChangeUpdate:
			current, err := organisationIn(tx, change.OrganisationID)
			if err != nil {
				return err
			}
			change.Before = current.OrganisationValues
			fields := differences(change.Before, change.After)
			if len(fields) == 0 {
				return ErrNothingChanged
			}
			change.Fields = strings.Join(fields, ",")
			if slices.Contains(fields, models.FieldParent) {
				if err := checkParent(tx, change.OrganisationID, change.After.ParentID); err != nil {
					return err
				}
			}
			if err := noConflict(tx, *change); err != nil {
				return err
			}

		case models.ChangeDelete:
			current, err := organisationIn(tx, change.OrganisationID)
			if err != nil {
				return err
			}
			change.Before = current.OrganisationValues
			change.After = models.OrganisationValues{}
			change.Fields = ""
			if err := organisationInUse(tx, change.OrganisationID); err != nil {
				return err
			}
			if err := noConflict(tx, *change); err != nil {
				return err
			}

		default:
			return errors.New("unknown kind of change")
		}

		return tx.Omit("Votes").Create(change).Error
	})
}

// differences lists the fields in which two sets of values differ.
func differences(before, after models.OrganisationValues) []string {
	var fields []string
	if before.Name != after.Name {
		fields = append(fields, models.FieldName)
	}
	if before.Kind != after.Kind {
		fields = append(fields, models.FieldKind)
	}
	if before.Website != after.Website {
		fields = append(fields, models.FieldWebsite)
	}
	if before.ParentID != after.ParentID {
		fields = append(fields, models.FieldParent)
	}
	if before.Location.Latitude != after.Location.Latitude ||
		before.Location.Longitude != after.Location.Longitude ||
		before.Location.Label != after.Location.Label {
		fields = append(fields, models.FieldPlace)
	}
	if before.LogoID != after.LogoID {
		fields = append(fields, models.FieldLogo)
	}
	return fields
}

// noConflict refuses a change while another pending one to the same
// organisation touches the same field, or would delete it.
//
// Two changes to one field would each be approved as replacing the value both
// were proposed against, and whichever was applied second would silently undo
// the first. A deletion conflicts with everything: approving a new logo for an
// organisation about to be deleted is a vote about nothing.
func noConflict(tx *gorm.DB, change models.OrganisationChange) error {
	var pending []models.OrganisationChange
	err := tx.Where("organisation_id = ? AND status = ?", change.OrganisationID, models.ChangePending).
		Find(&pending).Error
	if err != nil {
		return err
	}
	for _, other := range pending {
		if other.Kind == models.ChangeDelete || change.Kind == models.ChangeDelete {
			return ErrChangeConflict
		}
		for _, field := range change.FieldList() {
			if other.Sets(field) {
				return ErrChangeConflict
			}
		}
	}
	return nil
}

// Change returns one change with its votes, oldest vote first.
func (s *Store) Change(ctx context.Context, id string) (models.OrganisationChange, error) {
	return changeIn(s.db.WithContext(ctx), id)
}

func changeIn(tx *gorm.DB, id string) (models.OrganisationChange, error) {
	var change models.OrganisationChange
	err := tx.Preload("Votes", func(db *gorm.DB) *gorm.DB {
		return db.Order("created_at asc")
	}).First(&change, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.OrganisationChange{}, ErrChangeNotFound
	}
	return change, err
}

// ChangeQuery narrows a listing of changes.
type ChangeQuery struct {
	// Statuses limits the listing; empty means every status.
	Statuses []models.ChangeStatus

	// OrganisationID limits it to the changes about one organisation.
	OrganisationID string

	Page
}

// ListChanges returns changes with their votes, newest first.
func (s *Store) ListChanges(ctx context.Context, query ChangeQuery) ([]models.OrganisationChange, int64, error) {
	page := query.Page.clamp(100, 500)

	db := s.db.WithContext(ctx).Model(&models.OrganisationChange{})
	if len(query.Statuses) > 0 {
		db = db.Where("status IN ?", query.Statuses)
	}
	if query.OrganisationID != "" {
		db = db.Where("organisation_id = ?", query.OrganisationID)
	}

	var total int64
	if err := db.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var changes []models.OrganisationChange
	err := db.Preload("Votes", func(db *gorm.DB) *gorm.DB {
		return db.Order("created_at asc")
	}).Order("created_at desc, id asc").Limit(page.Limit).Offset(page.Offset).
		Find(&changes).Error
	return changes, total, err
}

// Outcome is what a vote or a withdrawal led to.
type Outcome struct {
	// Change is the change as it now stands, with every vote.
	Change models.OrganisationChange

	// Decided is whether this call closed it: applied, rejected, failed or
	// withdrawn.
	Decided bool

	// Unused are images nothing refers to any more — a logo replaced, the
	// upload of a change that will never be applied. The caller removes them
	// from storage; the store holds only their rows.
	Unused []string
}

// Vote records one editor's approval or rejection, and decides the change
// once it has enough of either.
//
// threshold is how many it takes either way. The same number for both, so
// neither side can end a change faster than the other: three people can let a
// change through, and three can stop it.
func (s *Store) Vote(ctx context.Context, changeID string, vote models.ChangeVote, threshold int) (Outcome, error) {
	if threshold < 1 {
		threshold = 1
	}

	var outcome Outcome
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		change, err := changeIn(tx, changeID)
		if err != nil {
			return err
		}
		if !change.Status.Open() {
			return ErrChangeClosed
		}
		if vote.Voter == "" || vote.Voter == change.Author {
			return ErrOwnChange
		}
		for _, cast := range change.Votes {
			if cast.Voter == vote.Voter {
				return ErrAlreadyVoted
			}
		}

		vote.ID = ""
		vote.ChangeID = change.ID
		if err := tx.Create(&vote).Error; err != nil {
			return err
		}
		change.Votes = append(change.Votes, vote)

		approvals, rejections := change.Tally()
		switch {
		case approvals >= threshold:
			outcome, err = decide(tx, change, models.ChangeApplied)
		case rejections >= threshold:
			outcome, err = decide(tx, change, models.ChangeRejected)
		default:
			outcome = Outcome{Change: change}
		}
		return err
	})
	return outcome, err
}

// WithdrawChange takes back a pending change. Only its author may.
func (s *Store) WithdrawChange(ctx context.Context, changeID, author string) (Outcome, error) {
	var outcome Outcome
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		change, err := changeIn(tx, changeID)
		if err != nil {
			return err
		}
		if !change.Status.Open() {
			return ErrChangeClosed
		}
		if author == "" || author != change.Author {
			return ErrNotAuthor
		}
		outcome, err = decide(tx, change, models.ChangeWithdrawn)
		return err
	})
	return outcome, err
}

// decide closes a pending change, carrying it out if it was approved.
//
// The status moves with a conditional update first: two last votes arriving
// together both count to the threshold, and only the one whose update finds
// the change still pending goes on to apply it. The other is recorded as a
// vote and does nothing else.
func decide(tx *gorm.DB, change models.OrganisationChange, status models.ChangeStatus) (Outcome, error) {
	now := time.Now().UTC()
	claimed := tx.Model(&models.OrganisationChange{}).
		Where("id = ? AND status = ?", change.ID, models.ChangePending).
		Updates(map[string]any{"status": status, "decided_at": now})
	if claimed.Error != nil {
		return Outcome{}, claimed.Error
	}
	if claimed.RowsAffected == 0 {
		fresh, err := changeIn(tx, change.ID)
		return Outcome{Change: fresh}, err
	}
	change.Status = status
	change.DecidedAt = &now

	outcome := Outcome{Change: change, Decided: true}
	if status != models.ChangeApplied {
		// The upload of a change that will never be applied is nobody's.
		if change.Sets(models.FieldLogo) && change.After.LogoID != "" {
			outcome.Unused = append(outcome.Unused, change.After.LogoID)
		}
		return outcome, nil
	}

	unused, err := apply(tx, change)
	if err == nil {
		outcome.Unused = unused
		return outcome, nil
	}
	if !refusal(err) {
		return Outcome{}, err
	}

	// Approved, and no longer possible. Recorded as that, with the reason,
	// rather than rolled back: the votes were cast and the people who cast
	// them should see what became of the change.
	change.Status = models.ChangeFailed
	change.Reason = err.Error()
	if err := tx.Model(&models.OrganisationChange{}).Where("id = ?", change.ID).
		Updates(map[string]any{"status": change.Status, "reason": change.Reason}).Error; err != nil {
		return Outcome{}, err
	}
	outcome.Change = change
	if change.Sets(models.FieldLogo) && change.After.LogoID != "" {
		outcome.Unused = append(outcome.Unused, change.After.LogoID)
	}
	return outcome, nil
}

// refusal reports whether an error is about the change no longer making
// sense, rather than about the database.
func refusal(err error) bool {
	for _, known := range []error{
		ErrOrganisationNotFound, ErrParentNotFound, ErrParentLoop, ErrOrganisationInUse,
	} {
		if errors.Is(err, known) {
			return true
		}
	}
	return false
}

// apply carries out an approved change, re-checking what it depends on. It
// returns the images the change left unused.
func apply(tx *gorm.DB, change models.OrganisationChange) ([]string, error) {
	switch change.Kind {
	case models.ChangeCreate:
		if err := checkParent(tx, change.OrganisationID, change.After.ParentID); err != nil {
			return nil, err
		}
		organisation := models.Organisation{OrganisationValues: change.After}
		organisation.ID = change.OrganisationID
		return nil, tx.Create(&organisation).Error

	case models.ChangeUpdate:
		organisation, err := organisationIn(tx, change.OrganisationID)
		if err != nil {
			return nil, err
		}
		var unused []string
		for _, field := range change.FieldList() {
			switch field {
			case models.FieldName:
				organisation.Name = change.After.Name
			case models.FieldKind:
				organisation.Kind = change.After.Kind
			case models.FieldWebsite:
				organisation.Website = change.After.Website
			case models.FieldParent:
				if err := checkParent(tx, organisation.ID, change.After.ParentID); err != nil {
					return nil, err
				}
				organisation.ParentID = change.After.ParentID
			case models.FieldPlace:
				organisation.Location = change.After.Location
			case models.FieldLogo:
				if organisation.LogoID != "" && organisation.LogoID != change.After.LogoID {
					unused = append(unused, organisation.LogoID)
				}
				organisation.LogoID = change.After.LogoID
			}
		}
		return unused, tx.Save(&organisation).Error

	case models.ChangeDelete:
		organisation, err := organisationIn(tx, change.OrganisationID)
		if err != nil {
			return nil, err
		}
		if err := organisationInUse(tx, organisation.ID); err != nil {
			return nil, err
		}
		var unused []string
		if organisation.LogoID != "" {
			unused = append(unused, organisation.LogoID)
		}

		// Whatever else was waiting for this organisation is now about
		// nothing. Closed as failed, with the reason, so the people who were
		// asked to vote on it are not left looking at a question nobody can
		// answer.
		var orphaned []models.OrganisationChange
		if err := tx.Where("organisation_id = ? AND status = ? AND id <> ?",
			organisation.ID, models.ChangePending, change.ID).Find(&orphaned).Error; err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		for _, other := range orphaned {
			if err := tx.Model(&models.OrganisationChange{}).Where("id = ?", other.ID).
				Updates(map[string]any{
					"status": models.ChangeFailed, "decided_at": now,
					"reason": ErrOrganisationNotFound.Error(),
				}).Error; err != nil {
				return nil, err
			}
			if other.Sets(models.FieldLogo) && other.After.LogoID != "" {
				unused = append(unused, other.After.LogoID)
			}
		}

		return unused, tx.Delete(&models.Organisation{}, "id = ?", organisation.ID).Error
	}
	return nil, errors.New("unknown kind of change")
}
