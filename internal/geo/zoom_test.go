package geo

import "testing"

// TestAPlaceIsNamedAsPreciselyAsItWasPinned.
//
// Somebody who zoomed to a building gets building-level detail; somebody who
// clicked a city view does not, because they did not name a building and an
// address invented for them would be precise and wrong.
func TestAPlaceIsNamedAsPreciselyAsItWasPinned(t *testing.T) {
	for _, tc := range []struct {
		mapZoom int
		want    int
	}{
		{4, 3},   // a country view is still a country
		{7, 5},   // a Land
		{9, 8},   // a Kreis
		{11, 10}, // a city — "Düsseldorf", where a budget is cut
		{14, 14}, // a suburb
		{16, 16}, // a street
		{18, 18}, // the building somebody actually pinned
	} {
		if asked := NominatimZoom(PlacePrecisionForZoom(tc.mapZoom)); asked != tc.want {
			t.Errorf("a place pinned at map zoom %d asks Nominatim for zoom %d, want %d",
				tc.mapZoom, asked, tc.want)
		}
	}
}

// TestPrecisionNeverGoesBackwards: zooming in must never produce a vaguer
// name than the view before it.
func TestPrecisionNeverGoesBackwards(t *testing.T) {
	previous := uint(0)
	for zoom := range 22 {
		precision := PlacePrecisionForZoom(zoom)
		if precision < previous {
			t.Errorf("zoom %d resolves to precision %d, coarser than zoom %d", zoom, precision, zoom-1)
		}
		if precision > PlacePrecision {
			t.Errorf("zoom %d resolves to precision %d, past what is stored", zoom, precision)
		}
		previous = precision
	}
}

// TestAPlaceIsStoredWhereItWasPinned: nothing here locates a person, so
// nothing is coarsened. A venue snapped to the centre of a geohash-6 cell
// lands up to 300 metres up the street, which for a demonstration's starting
// point is the wrong square.
func TestAPlaceIsStoredWhereItWasPinned(t *testing.T) {
	// Rathaus Düsseldorf, Marktplatz 2.
	const lat, lng = 51.225890, 6.772410

	hash := Encode(lat, lng)
	if len(hash) != StoredPrecision {
		t.Fatalf("hash %q is not stored at full precision", hash)
	}
	backLat, backLng := Decode(hash)
	if Distance(lat, lng, backLat, backLng) > 1 {
		t.Errorf("a place's own geohash does not round-trip to within a metre")
	}
}

// TestACountryViewIsNotAPoint: a pin dropped on an untouched map of Germany
// names Germany. Drawing it would put a federal reform in a field in
// Thuringia.
func TestACountryViewIsNotAPoint(t *testing.T) {
	for _, zoom := range []int{0, 3, 5, 6, 7} {
		if PlacePrecisionForZoom(zoom) >= MappablePrecision {
			t.Errorf("zoom %d is treated as a point; a country or a Land is a name", zoom)
		}
	}
	for _, zoom := range []int{8, 10, 13, 18} {
		if PlacePrecisionForZoom(zoom) < MappablePrecision {
			t.Errorf("zoom %d is refused a point; a Kreis and anything finer is one", zoom)
		}
	}
}
