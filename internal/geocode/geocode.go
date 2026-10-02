// Package geocode turns coordinates into the name of a place.
//
// It talks to Nominatim (nominatim.org), the OpenStreetMap geocoder, and it
// does so **from the backend, never from the browser**. That is the whole
// shape of this package: a lookup made by somebody's own browser would hand
// their IP address to a third party, and no page of this site — the console
// included — makes a request to anybody else. The backend asks on their
// behalf, so Nominatim learns that this site looked up a cell, and nothing
// about who.
//
// Two further consequences follow from that decision and are implemented here:
// the lookup is cached by cell, so the same square is asked about once rather
// than once per action announced on it; and it is rate limited, because
// Nominatim's usage policy allows one request a second and a site that ignored
// that would be cut off precisely when it got busy.
package geocode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/geo"
)

// DefaultEndpoint is the public Nominatim instance.
//
// It is a default, not a constant: the usage policy for the public server
// forbids heavy use, so any real deployment points this at its own instance.
// Changing that is configuration, never a code change.
const DefaultEndpoint = "https://nominatim.openstreetmap.org"

// minInterval is the gap the public instance's usage policy requires.
const minInterval = time.Second

// lookupTimeout bounds one call. A place name is a nicety; saving an action
// must never wait on it.
const lookupTimeout = 5 * time.Second

// Place is what a lookup yields.
type Place struct {
	// Label is what a reader sees: "Düsseldorf", "Köln, Ehrenfeld",
	// "Rathaus, Marktplatz 2, Düsseldorf", depending on how far the editor who
	// pinned it had zoomed in.
	Label string

	// CountryCode is the ISO 3166-1 alpha-2 code.
	CountryCode string
}

// Geocoder resolves coordinates to a place name.
type Geocoder interface {
	Reverse(ctx context.Context, lat, lng float64, precision uint) (Place, error)
}

// Nominatim is the OpenStreetMap-backed implementation.
type Nominatim struct {
	endpoint  string
	userAgent string
	http      *http.Client

	// mu guards both the cache and the rate limiter, which are the two pieces
	// of shared state two concurrent saves touch.
	mu       sync.Mutex
	cache    map[string]Place
	lastCall time.Time
}

// New returns a geocoder.
//
// userAgent is required by Nominatim's usage policy: a request without one
// identifying the application is blocked, and rightly — an anonymous flood is
// indistinguishable from abuse.
func New(endpoint, userAgent string) *Nominatim {
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	return &Nominatim{
		endpoint:  endpoint,
		userAgent: userAgent,
		http:      &http.Client{Timeout: lookupTimeout},
		cache:     map[string]Place{},
	}
}

// Reverse names the place a point falls in, at the requested granularity.
func (n *Nominatim) Reverse(ctx context.Context, lat, lng float64, precision uint) (Place, error) {
	// The cache key is the cell, not the point: everything pinned on the same
	// town resolves to the same answer, so the second one should not cost a
	// request. This is also what keeps a busy day inside the rate limit.
	key := geo.Prefix(geo.Encode(lat, lng), precision) + "/" + strconv.Itoa(int(precision))

	n.mu.Lock()
	if place, ok := n.cache[key]; ok {
		n.mu.Unlock()
		return place, nil
	}
	n.mu.Unlock()

	place, err := n.fetch(ctx, lat, lng, precision)
	if err != nil {
		return Place{}, err
	}

	n.mu.Lock()
	n.cache[key] = place
	n.mu.Unlock()
	return place, nil
}

func (n *Nominatim) fetch(ctx context.Context, lat, lng float64, precision uint) (Place, error) {
	waited := n.wait(ctx)

	query := url.Values{}
	query.Set("lat", strconv.FormatFloat(lat, 'f', 6, 64))
	query.Set("lon", strconv.FormatFloat(lng, 'f', 6, 64))
	// Nominatim's own zoom decides how much address detail comes back. Asking
	// for a coarse level is not merely a display choice: a street we never
	// requested is a street we never hold.
	query.Set("zoom", strconv.Itoa(geo.NominatimZoom(precision)))
	query.Set("format", "jsonv2")
	query.Set("addressdetails", "1")

	endpoint := n.endpoint + "/reverse?" + query.Encode()

	// TRACE, because this is a data dump: the request as it goes out, per
	// lookup. Never on in production.
	log.Trace().
		Str("url", endpoint).
		Str("user_agent", n.userAgent).
		Dur("rate_limit_wait", waited).
		Msg("nominatim: request")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Place{}, fmt.Errorf("geocode: build request: %w", err)
	}
	req.Header.Set("User-Agent", n.userAgent)
	req.Header.Set("Accept", "application/json")

	started := time.Now()
	resp, err := n.http.Do(req)
	if err != nil {
		log.Trace().Str("url", endpoint).Err(err).Msg("nominatim: call failed")
		return Place{}, fmt.Errorf("geocode: call nominatim: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	// Read the body out rather than decoding from the stream, so the raw
	// answer can be logged. Without it, debugging a wrong label means guessing
	// which of Nominatim's twenty address fields came back.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return Place{}, fmt.Errorf("geocode: read answer: %w", err)
	}

	log.Trace().
		Str("url", endpoint).
		Int("status", resp.StatusCode).
		Dur("took", time.Since(started)).
		Str("response_body", string(body)).
		Msg("nominatim: response")

	if resp.StatusCode != http.StatusOK {
		return Place{}, fmt.Errorf("geocode: nominatim answered %s", resp.Status)
	}

	var payload struct {
		Address map[string]string `json:"address"`
		Name    string            `json:"name"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return Place{}, fmt.Errorf("geocode: decode answer: %w", err)
	}

	place := Place{
		Label:       label(payload.Address, precision, payload.Name),
		CountryCode: upper(payload.Address["country_code"]),
	}

	// DEBUG carries the decision without the dump: which address fields were
	// available and which label was built from them is what you need when a
	// place reads wrong, and it is safe to leave on while tuning.
	log.Debug().
		Uint("precision", precision).
		Int("nominatim_zoom", geo.NominatimZoom(precision)).
		Str("label", place.Label).
		Str("country", place.CountryCode).
		Strs("address_fields", addressKeys(payload.Address)).
		Msg("nominatim: named a place")
	return place, nil
}

// maxResponseBytes bounds one answer. A reverse lookup returns an address, not
// a document; anything larger is not something to hold in memory or a log.
const maxResponseBytes = 64 << 10

// addressKeys lists what came back, sorted, so a log line shows which fields
// were on offer when a label was chosen.
func addressKeys(address map[string]string) []string {
	keys := make([]string, 0, len(address))
	for key := range address {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// wait spaces requests out to honour the usage policy, and reports how long it
// held the call back — a save that felt slow is usually this.
func (n *Nominatim) wait(ctx context.Context) time.Duration {
	n.mu.Lock()
	gap := time.Until(n.lastCall.Add(minInterval))
	n.lastCall = time.Now().Add(max(gap, 0))
	n.mu.Unlock()

	if gap <= 0 {
		return 0
	}
	timer := time.NewTimer(gap)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
	return gap
}

// label builds the name to show at a given granularity.
//
// At district level it is **the town and then the district** — "Düsseldorf,
// Stadtbezirk 7", not "Stadtbezirk 7". A district name alone is unreadable to
// anybody who does not already live there, which defeats the purpose: the
// label exists so a reader knows where something is happening, and a site
// organised by place cannot afford a place nobody can locate.
//
// The road only enters the name past district level, where the editor zoomed
// far enough to have meant an address.
func label(address map[string]string, precision uint, fallback string) string {
	first := func(keys ...string) string {
		for _, key := range keys {
			if value := address[key]; value != "" {
				return value
			}
		}
		return ""
	}

	town := first("city", "town", "village", "municipality")

	switch precision {
	case 2:
		return orFallback(first("country"), fallback)
	case 3:
		return orFallback(first("state", "region", "country"), fallback)
	case 4:
		return orFallback(first("county", "state_district", "state", "country"), fallback)
	case 5:
		return orFallback(first("city", "town", "village", "municipality",
			"county", "state"), fallback)
	}

	district := first("city_district", "suburb", "borough", "quarter")

	// A quarter within its town. Nominatim calls a Paris arrondissement a
	// city_district and a Düsseldorf Stadtbezirk the same.
	if precision <= geo.DistrictPrecision {
		switch {
		case town != "" && district != "" && district != town:
			return town + ", " + district
		case town != "":
			return town
		case district != "":
			return district
		}
		return fallback
	}

	// Past that we are naming a venue rather than an area, and the road comes
	// into the name.
	//
	// The town is always appended, for the reason the district label carries
	// it too: "Bilker Straße 46" is unreadable to anybody who does not already
	// live there, and somebody deciding whether to come to a demonstration is
	// by definition not there yet.
	street := address["road"]
	if number := address["house_number"]; street != "" && number != "" {
		street += " " + number
	}

	// Nominatim's `name` at building zoom is the venue itself — "Destille",
	// "Salle des fêtes". It is the most useful thing on the page when it
	// exists, because it is what will be written on the door.
	var venue string
	if precision >= 11 {
		venue = fallback
	}

	return joinPlace(venue, street, orFallback(town, district))
}

// joinPlace assembles the parts of a venue label, skipping the empty ones and
// never repeating a part that is already there.
func joinPlace(parts ...string) string {
	var out []string
	for _, part := range parts {
		if part == "" {
			continue
		}
		if slices.Contains(out, part) {
			continue
		}
		out = append(out, part)
	}
	return strings.Join(out, ", ")
}

func orFallback(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func upper(code string) string {
	if len(code) != 2 {
		return ""
	}
	return string([]byte{upperByte(code[0]), upperByte(code[1])})
}

func upperByte(b byte) byte {
	if b >= 'a' && b <= 'z' {
		return b - 32
	}
	return b
}
