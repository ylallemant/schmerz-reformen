package models

import "time"

// AuditAction is what somebody signed in to the console did.
type AuditAction string

const (
	AuditCreate  AuditAction = "create"
	AuditUpdate  AuditAction = "update"
	AuditDelete  AuditAction = "delete"
	AuditPublish AuditAction = "publish"
	AuditArchive AuditAction = "archive"
	AuditCancel  AuditAction = "cancel"

	// AuditConfigChange is a change to the installation rather than to
	// content: the active theme, an uploaded image.
	AuditConfigChange AuditAction = "config_change"

	// The curation of organisations: a change proposed, a vote on it, a
	// change taken back by its author. When a change is applied, its effect
	// is recorded as a create, update or delete against its author's name.
	AuditPropose  AuditAction = "propose"
	AuditApprove  AuditAction = "approve"
	AuditReject   AuditAction = "reject"
	AuditWithdraw AuditAction = "withdraw"
)

// AuditEntry records one write made through the console.
//
// The log is append-only: never updated, never deleted. Content here is
// published without review — a collective's editors are trusted because their
// collective vouched for them — and this is the other half of that bargain:
// every change a reader can see traces back to a named person, including the
// ones that were later undone.
//
// It is a record about staff, not about readers. Nothing a signed-in reader
// does is ever written here.
type AuditEntry struct {
	// No embedded Model: an audit entry is written once and never updated, so
	// it has a creation time and no modification time.
	ID        string    `gorm:"primaryKey;size:36" json:"id"`
	CreatedAt time.Time `gorm:"index" json:"created_at"`

	// Actor is the editor's OIDC subject — the stable identifier, which
	// survives a change of name or address in the identity provider.
	Actor string `gorm:"index;size:256" json:"actor"`

	// ActorName is what the identity provider called them at the time. Kept
	// beside the subject because a log of opaque identifiers is one nobody
	// reads, and frozen here because it is a record of who somebody was when
	// they did it.
	ActorName string `gorm:"size:256" json:"actor_name,omitempty"`

	Action AuditAction `gorm:"index;size:32" json:"action"`

	// SubjectType and SubjectID say what was acted on: "collective", "member",
	// "organisation", "topic", "update", "action", "theme".
	SubjectType string `gorm:"index;size:32" json:"subject_type"`
	SubjectID   string `gorm:"index;size:36" json:"subject_id"`

	// CollectiveID is whose content it was, so a collective's own history can
	// be read without everybody else's. Empty for installation-wide changes.
	CollectiveID string `gorm:"index;size:36" json:"collective_id,omitempty"`

	// Summary is what the thing was called when it was changed — a title, a
	// name. It is what makes the entry for a deleted topic readable after the
	// topic is gone.
	Summary string `gorm:"size:256" json:"summary,omitempty"`
}

// TableName keeps the plural consistent with the other tables.
func (AuditEntry) TableName() string { return "audit_entries" }
