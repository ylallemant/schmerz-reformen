package models

import "time"

// ActionKind says what people are being asked to come to.
type ActionKind string

const (
	ActionDemonstration ActionKind = "demonstration"
	ActionRally         ActionKind = "rally"   // Kundgebung
	ActionStrike        ActionKind = "strike"  // Streik
	ActionMeeting       ActionKind = "meeting" // Treffen, Plenum
	ActionInfo          ActionKind = "info"    // Infostand, Veranstaltung
	ActionCouncil       ActionKind = "council" // a session where the decision is taken
	ActionOther         ActionKind = "other"
)

// ActionKinds lists them in the order a form and a filter offer them.
var ActionKinds = []ActionKind{
	ActionDemonstration, ActionRally, ActionStrike, ActionMeeting,
	ActionInfo, ActionCouncil, ActionOther,
}

// Valid reports whether the kind is one the model defines.
func (k ActionKind) Valid() bool {
	for _, known := range ActionKinds {
		if k == known {
			return true
		}
	}
	return false
}

// Action is something happening at a time and a place: the entry the calendar
// is made of.
type Action struct {
	Model

	CollectiveID string `gorm:"index;size:36" json:"collective_id"`

	// TopicID links the action to what it is about. Optional — a collective's
	// monthly meeting is about all of it.
	TopicID string `gorm:"index;size:36" json:"topic_id,omitempty"`

	Kind  ActionKind `gorm:"index;size:16" json:"kind"`
	Title string     `gorm:"size:200" json:"title"`

	Description string `gorm:"type:text" json:"description,omitempty"`

	// StartsAt is an instant, stored in UTC. The wall-clock time an editor
	// typed is read in the installation's time zone before it gets here, and
	// turned back into one on the way out; a stored wall-clock would be an
	// hour wrong twice a year for every feed reader and calendar that
	// subscribes.
	StartsAt time.Time `gorm:"index" json:"starts_at"`

	// EndsAt is optional. A demonstration ends when people go home.
	EndsAt *time.Time `json:"ends_at,omitempty"`

	// Location is the venue, stored exactly as pinned: somebody has to be able
	// to walk to it.
	Location Location `gorm:"embedded;embeddedPrefix:location_" json:"location"`

	// ExternalURL is the organiser's own page for it, where there is one.
	ExternalURL string `gorm:"size:512" json:"external_url,omitempty"`

	Status PublishStatus `gorm:"index;size:16" json:"status"`

	// Cancelled marks a published action as called off.
	//
	// A flag rather than a status, and never a deletion: people said they were
	// coming, put it in their calendars and told their colleagues. An action
	// that vanished would leave them standing in a square; one that says
	// "cancelled" in every place it used to appear does not.
	Cancelled bool `json:"cancelled"`

	PublishedAt *time.Time `gorm:"index" json:"published_at,omitempty"`
}
