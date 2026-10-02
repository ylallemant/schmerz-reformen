// Package models is the persisted domain: collectives and the organisations in
// them, the topics they campaign on, the updates and actions they publish,
// reader accounts, and the audit log.
package models

import (
	"time"

	"gorm.io/gorm"
)

// PublishStatus is where a piece of content stands. Everything an editor
// writes — a collective's profile, a topic, an update, an action — travels the
// same states.
//
// There is no review step between them, and that is deliberate: content is
// written by people a collective vouched for through its identity-provider
// group, and their name is in the audit log against every change. The draft
// state exists so that something can be prepared before it is public, not so
// that somebody else can approve it.
type PublishStatus string

const (
	// StatusDraft is visible in the console and nowhere else.
	StatusDraft PublishStatus = "draft"

	// StatusPublished is public.
	StatusPublished PublishStatus = "published"

	// StatusArchived is public but over: a cut that was withdrawn or voted
	// through, a collective that wound itself up. It leaves the map and the
	// listings and keeps its page, because the links people shared must not
	// start answering 404 on the day a fight ends.
	StatusArchived PublishStatus = "archived"
)

// Valid reports whether the status is one the model defines.
func (s PublishStatus) Valid() bool {
	switch s {
	case StatusDraft, StatusPublished, StatusArchived:
		return true
	}
	return false
}

// Public reports whether a reader may open the page.
func (s PublishStatus) Public() bool {
	return s == StatusPublished || s == StatusArchived
}

// Location is a point pinned on a map. Kept as plain coordinates rather than a
// PostGIS type so SQLite and PostgreSQL behave identically; proximity is a
// geohash prefix search refined by a distance check in Go.
//
// Every location here is a published place — a town hall, a square, the city a
// budget belongs to — and is stored exactly as pinned. Nothing in this model
// locates a person.
type Location struct {
	Latitude  float64 `gorm:"index" json:"latitude"`
	Longitude float64 `gorm:"index" json:"longitude"`

	// Geohash is the coordinates as a prefix-searchable string, so "near
	// here" is an indexed string comparison that PostgreSQL and SQLite answer
	// identically. It is derived, never supplied: every write recomputes it
	// from the coordinates, because a geohash that disagrees with its own
	// latitude is a silent wrong answer rather than a visible error.
	Geohash string `gorm:"index;size:12" json:"geohash,omitempty"`

	// Label is what to show a human: a town, a square, an address. Never used
	// to search.
	Label string `json:"label,omitempty"`

	// CountryCode is the ISO 3166-1 alpha-2 code, for coarse filtering.
	CountryCode string `gorm:"size:2;index" json:"country_code,omitempty"`
}

// Placed reports whether the location is a point the map can draw.
func (l Location) Placed() bool { return l.Geohash != "" }

// Model is the base every persisted type embeds. IDs are UUID strings so that
// a record's public identity never depends on insertion order.
type Model struct {
	ID        string    `gorm:"primaryKey;size:36" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// BeforeCreate assigns an identifier when one was not set explicitly.
func (m *Model) BeforeCreate(*gorm.DB) error {
	if m.ID == "" {
		m.ID = NewID()
	}
	return nil
}
