package backend

import (
	"context"

	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/geo"
	"github.com/ylallemant/schmerz-reformen/internal/models"
)

// PlaceInput is a place as an editor's form sends it.
type PlaceInput struct {
	Latitude  float64 `json:"latitude,omitempty" doc:"where the pin was dropped; zero for no pin"`
	Longitude float64 `json:"longitude,omitempty"`

	// Zoom is the map's zoom when the pin was dropped. It says how precise the
	// editor meant to be, and so how much of an address the name carries.
	Zoom int `json:"zoom,omitempty" doc:"the map zoom the pin was dropped at"`

	// Place is the name as the editor wants it shown. Empty asks the backend
	// to name the point itself.
	Place string `json:"place,omitempty" doc:"the place name to show; empty has the pin named by reverse geocoding"`
}

// resolvePlace turns what a form sent into a stored location.
//
// # The pin is stored where it was put
//
// Every place here is a published place — the city a budget belongs to, the
// square a demonstration starts from — and none of them locates a person, so
// nothing is coarsened. See internal/geo.
//
// # A country view is not a point
//
// A pin dropped on an untouched map of Germany names Germany. Below
// geo.MappablePrecision the name is kept and the coordinates are not: a
// federal reform drawn as a marker in the geometric middle of the country
// would tell every reader it is happening there.
//
// # The editor's own name for the place wins
//
// Nominatim knows "Marktplatz 2, Düsseldorf". The editor knows it is "vor dem
// Rathaus", which is what goes on the flyer. The geocoder is asked only when
// nobody typed a name — and then not at all, rather than asked and ignored.
//
// # An unmoved pin is not looked up again
//
// Saving a topic to fix a typo must not cost a request to a third party, nor
// replace a name somebody chose with the one Nominatim would have picked.
func (a *API) resolvePlace(ctx context.Context, in PlaceInput, previous models.Location) (models.Location, error) {
	label, err := cleanLine("the place", in.Place, maxPlaceRunes)
	if err != nil {
		return models.Location{}, err
	}

	// No pin at all: a name on its own, or nothing.
	if in.Latitude == 0 && in.Longitude == 0 {
		return models.Location{Label: label}, nil
	}

	if in.Latitude == previous.Latitude && in.Longitude == previous.Longitude {
		kept := previous
		if label != "" {
			kept.Label = label
		}
		return kept, nil
	}

	precision := geo.PlacePrecisionForZoom(in.Zoom)
	location := models.Location{Label: label}
	if precision >= geo.MappablePrecision {
		location.Latitude, location.Longitude = in.Latitude, in.Longitude
	}

	// Named by the editor: there is nothing left to ask anybody. This is also
	// what keeps a batch of places entered with their names — an import, the
	// local runner's fixtures — from becoming a batch of requests to a third
	// party whose usage policy allows one a second.
	if a.geocoder == nil || label != "" {
		return location, nil
	}

	// A name is a nicety and never blocks a save: if Nominatim is slow or
	// down, the place is stored as the editor left it.
	named, err := a.geocoder.Reverse(ctx, in.Latitude, in.Longitude, precision)
	if err != nil {
		log.Warn().Err(err).Msg("reverse geocoding failed; storing the place as given")
		return location, nil
	}
	location.Label = named.Label
	location.CountryCode = named.CountryCode
	return location, nil
}
