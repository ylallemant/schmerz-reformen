package store

import (
	"gorm.io/gorm"

	"github.com/ylallemant/schmerz-reformen/internal/geo"
)

// Page bounds a listing.
type Page struct {
	Limit  int
	Offset int
}

// clamp gives a listing a size when the caller named none, and a ceiling when
// the caller named too much. A listing with no limit is one a single request
// can use to read the whole table.
func (p Page) clamp(fallback, ceiling int) Page {
	if p.Limit <= 0 {
		p.Limit = fallback
	}
	if p.Limit > ceiling {
		p.Limit = ceiling
	}
	if p.Offset < 0 {
		p.Offset = 0
	}
	return p
}

// inViewport narrows a query to rows whose pin may fall inside a map viewport.
//
// It returns the box to trim against afterwards, or nil when there is nothing
// to trim. Geohash cells always cover at least the viewport, so they overhang
// its edges: a row in Kassel answers a viewport over the Ruhr when both sit
// under one coarse prefix. The query therefore over-reads and every caller
// finishes with box.Contains in Go — which is why the box comes back rather
// than being consumed here.
//
// A nil box, or one whose cover is empty, is the whole world, and narrowing to
// everywhere is not narrowing.
//
// Rows with no pin are excluded whenever a viewport is given: something with
// no place is not on a map, whatever the map shows.
func (s *Store) inViewport(db *gorm.DB, box *geo.Box) (*gorm.DB, *geo.Box) {
	if box == nil {
		return db, nil
	}
	db = db.Where("location_geohash <> ?", "")

	cells := geo.Cover(*box)
	if len(cells) == 0 {
		return db, nil
	}

	prefixes := s.db.Session(&gorm.Session{NewDB: true})
	for i, cell := range cells {
		if i == 0 {
			prefixes = prefixes.Where("location_geohash LIKE ?", cell+"%")
			continue
		}
		prefixes = prefixes.Or("location_geohash LIKE ?", cell+"%")
	}
	return db.Where(prefixes), box
}
