package models

import (
	"gorm.io/gorm"

	"github.com/ylallemant/schmerz-reformen/internal/geo"
)

// Refresh recomputes the derived geohash from the coordinates.
//
// Nothing outside this file should ever set Geohash. It is derived data, and
// the one failure mode worth designing against is a geohash that no longer
// matches the point it claims to describe: the row would then be invisible to
// a search of its own neighbourhood and would surface in somebody else's,
// with nothing to indicate anything was wrong.
func (l *Location) Refresh() {
	if l == nil {
		return
	}
	if l.Latitude == 0 && l.Longitude == 0 {
		// Null Island is not a place anybody pinned; treat it as "no location"
		// rather than encoding a point in the Gulf of Guinea.
		l.Geohash = ""
		return
	}
	l.Geohash = geo.Encode(l.Latitude, l.Longitude)
}

// The hooks below are what make "derived, never supplied" true. Every write
// path — the API, a seed, a test — goes through GORM, so putting the
// recomputation here means no caller can forget it and no future endpoint can
// introduce a stale hash.

// BeforeSave keeps a collective's geohash and slug in step with what it is.
func (c *Collective) BeforeSave(*gorm.DB) error {
	c.Location.Refresh()
	// Normalised here rather than at the call site, so a seed or a test cannot
	// produce a collective whose address differs from its own slug rule — two
	// collectives one capital letter apart would be two pages nobody can tell
	// apart in a link.
	c.Slug = Slugify(c.Slug)
	return nil
}

// BeforeSave keeps a topic's geohash in step with its coordinates.
func (t *Topic) BeforeSave(*gorm.DB) error {
	t.Location.Refresh()
	return nil
}

// BeforeSave keeps an action's geohash in step with its coordinates.
func (a *Action) BeforeSave(*gorm.DB) error {
	a.Location.Refresh()
	return nil
}

// BeforeSave keeps an organisation's geohash in step with its coordinates.
func (o *Organisation) BeforeSave(*gorm.DB) error {
	o.Location.Refresh()
	return nil
}
