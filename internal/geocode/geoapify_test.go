package geocode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/ylallemant/schmerz-reformen/internal/geo"
)

// TestGeoapifyNamesAVenueTheWayNominatimDoes.
//
// Every place here is a published one, so a pin dropped on a building is named
// after the building and its street — whichever provider the queue happened
// to ask. Doléances, where this provider came from, stops it at the town; a
// copy that kept that would name a demonstration "Düsseldorf" when Nominatim
// would have said "Marktplatz 2".
func TestGeoapifyNamesAVenueTheWayNominatimDoes(t *testing.T) {
	var mu sync.Mutex
	var asked url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = r.URL.Query()
		mu.Unlock()
		if agent := r.Header.Get("User-Agent"); agent != "schmerz-reformen/test (a@example.org)" {
			t.Errorf("User-Agent %q, want this site's", agent)
		}
		json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{ //nolint:errcheck
			"name": "Rathaus", "street": "Marktplatz", "housenumber": "2",
			"suburb": "Altstadt", "city": "Düsseldorf", "country_code": "de",
		}}})
	}))
	t.Cleanup(server.Close)
	provider := NewGeoapify(server.URL, "a-key", "schmerz-reformen/test (a@example.org)")

	place, err := provider.Reverse(context.Background(), 51.2259, 6.7724, geo.PlacePrecision)
	if err != nil {
		t.Fatalf("Reverse: %v", err)
	}
	if place.Label != "Rathaus, Marktplatz 2, Düsseldorf" || place.CountryCode != "DE" {
		t.Errorf("a building = %+v", place)
	}
	mu.Lock()
	if asked.Has("type") {
		t.Errorf("a building was asked about as a %q, which answers with an area", asked.Get("type"))
	}
	mu.Unlock()

	// A quarter: the town and the quarter, and the street that came with
	// the answer is not in the name.
	place, err = provider.Reverse(context.Background(), 51.2259, 6.7724, geo.DistrictPrecision)
	if err != nil {
		t.Fatalf("Reverse: %v", err)
	}
	if place.Label != "Düsseldorf, Altstadt" {
		t.Errorf("a quarter = %q", place.Label)
	}
}

// TestGeoapifyIsAskedForTheDetailThePinMeant.
func TestGeoapifyIsAskedForTheDetailThePinMeant(t *testing.T) {
	for precision, want := range map[uint]string{
		2:                     "country",
		3:                     "state",
		4:                     "county",
		5:                     "city",
		geo.DistrictPrecision: "street",
		9:                     "street",
		geo.PlacePrecision:    "",
	} {
		if got := geoapifyType(precision); got != want {
			t.Errorf("precision %d asks for %q, want %q", precision, got, want)
		}
	}
}
