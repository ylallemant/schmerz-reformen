package models

import "time"

// PushSubscription is one browser on one device that has agreed to be told
// things.
//
// It is the successor to an email address and it is a better one: an endpoint
// issued by a push service is a way to reach a browser, not a way to identify
// a person. It cannot be searched for, written to by anybody but this server,
// or correlated with anything outside this site — which is why it is
// compatible with an account that holds nothing else.
//
// One account has several, and they are **not** the same set as its passkeys:
// a synced passkey signs somebody in on a device that has never been asked for
// permission. "Signed in here" and "notified here" are different facts, and
// the account page shows them as two lists for that reason.
type PushSubscription struct {
	Model

	AccountID string `gorm:"index;size:36" json:"-"`

	// Endpoint is the push service's address for this browser. Unique: a
	// browser that re-subscribes gets the same endpoint back, and two rows for
	// it would send everything twice to one screen.
	Endpoint string `gorm:"uniqueIndex;size:512" json:"-"`

	// P256dh and Auth are the browser's own keys. The payload is encrypted to
	// them, so the push service carries the message without being able to read
	// it — which is what keeps what somebody follows private
	// from the company delivering it.
	P256dh string `gorm:"size:255" json:"-"`
	Auth   string `gorm:"size:64" json:"-"`

	// Label is how the person recognises this device in their own list.
	Label string `gorm:"size:128" json:"label"`

	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// NotificationKind is what happened.
type NotificationKind string

const (
	// NotifyTopicUpdate is news on a topic the reader follows, or from a
	// collective they follow.
	NotifyTopicUpdate NotificationKind = "topic_update"

	// NotifyTopicPublished is a collective the reader follows taking up
	// something new.
	NotifyTopicPublished NotificationKind = "topic_published"

	// NotifyActionPublished is an action announced by a collective, or on a
	// topic, the reader follows.
	NotifyActionPublished NotificationKind = "action_published"

	// NotifyActionChanged says an action the reader meant to come to has moved
	// in time or place, so the plan they made is no longer the one on offer.
	NotifyActionChanged NotificationKind = "action_changed"

	// NotifyActionCancelled says it is not happening at all.
	NotifyActionCancelled NotificationKind = "action_cancelled"
)

// Notification is one thing worth telling somebody, held so that it survives
// whether or not a push reached them.
//
// **The list is the channel; the push is a tap on the shoulder.** An iPhone
// that has not installed the site receives no push at all, a permission can be
// refused, and an endpoint can die between one week and the next — so nothing
// may exist only as a push. Everything is written here first and delivered
// second, and somebody who never allows a notification still finds out by
// coming back.
type Notification struct {
	Model

	AccountID string           `gorm:"index;size:36" json:"-"`
	Kind      NotificationKind `gorm:"size:32;index" json:"kind"`

	// Subject is what it is about — a topic, an update, an action — so the
	// item can link somewhere rather than being a sentence with no door.
	SubjectType string `gorm:"size:32" json:"subject_type,omitempty"`
	SubjectID   string `gorm:"size:36;index" json:"subject_id,omitempty"`

	// Title and Body are stored rendered, in the language the reader had when
	// it was made.
	//
	// Rendering at read time would be tidier and would also mean a
	// notification silently changing its words when a catalogue is edited, or
	// losing them when a key is renamed. A notification is a record of
	// something that was said at a moment; it is written down.
	Title string `gorm:"size:256" json:"title"`
	Body  string `gorm:"size:512" json:"body,omitempty"`

	ReadAt *time.Time `gorm:"index" json:"read_at,omitempty"`
}
