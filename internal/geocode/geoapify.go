package geocode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"
)

// GeoapifyEndpoint is the service's own address.
const GeoapifyEndpoint = "https://api.geoapify.com/v1"

// geoapifyFreeTier is what Geoapify allows without paying: five requests a
// second and three thousand a day. Every paid plan raises both, which is why
// these are defaults rather than constants the code relies on.
var geoapifyFreeTier = Limits{PerSecond: 5, PerDay: 3000}

// Geoapify is a commercial reverse geocoder, used beside Nominatim rather than
// instead of it.
//
// # Why a second provider exists at all
//
// Nominatim's public instance allows one request a second and its usage policy
// forbids heavy use, so a site that gets busy has two honest options: run its
// own instance, or pay somebody. This is the second, and running both is
// the point — see Service, where the queue spans them and the rates add.
//
// The key is configuration and nothing else: no account, no SDK, one query
// parameter. It is sent in the query rather than the header because that is
// what their API documents first, and because a header buys nothing here —
// the URL never leaves this process.
type Geoapify struct {
	endpoint string
	apiKey   string
	http     *http.Client
}

// NewGeoapify returns a provider, or nil when no key is configured.
//
// Nil rather than an error: a deployment that configured no key has not asked
// for this provider, and the queue simply has one fewer. Refusing to start
// would make an optional provider mandatory.
//
// **The return type is the interface, deliberately.** A `*Geoapify` nil put
// into a `Provider` is not a nil interface — it carries the type — so the
// queue's own `provider == nil` check would pass it through and call a method
// on nothing. Returning the interface is what makes the absent provider
// actually absent.
func NewGeoapify(endpoint, apiKey string) Provider {
	if apiKey == "" {
		return nil
	}
	if endpoint == "" {
		endpoint = GeoapifyEndpoint
	}
	return &Geoapify{
		endpoint: endpoint,
		apiKey:   apiKey,
		http:     &http.Client{Timeout: lookupTimeout},
	}
}

// Name identifies it in logs and configuration.
func (g *Geoapify) Name() string { return GeoapifyName }

// Defaults are the free tier's limits, used until a deployment says otherwise.
func (g *Geoapify) Defaults() Limits { return geoapifyFreeTier }

// Reverse names the place a point falls in.
func (g *Geoapify) Reverse(ctx context.Context, lat, lng float64, precision uint) (Place, error) {
	query := url.Values{}
	query.Set("lat", strconv.FormatFloat(lat, 'f', 6, 64))
	query.Set("lon", strconv.FormatFloat(lng, 'f', 6, 64))
	query.Set("format", "json")
	query.Set("limit", "1")
	// The granularity is asked for, the way the Nominatim zoom asks for it:
	// a pin dropped on a city should be named after the city, not after the
	// nearest house.
	if kind := geoapifyType(precision); kind != "" {
		query.Set("type", kind)
	}
	query.Set("apiKey", g.apiKey)

	endpoint := g.endpoint + "/geocode/reverse?" + query.Encode()

	// TRACE, and with the key stripped: the URL carries a credential.
	log.Trace().
		Str("url", g.endpoint+"/geocode/reverse").
		Float64("lat", lat).Float64("lng", lng).
		Str("type", geoapifyType(precision)).
		Msg("geoapify: request")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Place{}, fmt.Errorf("geocode: build geoapify request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	started := time.Now()
	resp, err := g.http.Do(req)
	if err != nil {
		return Place{}, fmt.Errorf("geocode: call geoapify: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return Place{}, fmt.Errorf("geocode: read geoapify answer: %w", err)
	}

	log.Trace().
		Int("status", resp.StatusCode).
		Dur("took", time.Since(started)).
		Str("response_body", string(body)).
		Msg("geoapify: response")

	// A 429 is reported as itself. The queue treats it differently from an
	// ordinary failure: backed off rather than retried immediately, and after
	// five minutes of them taken as the day's allowance being gone — which is
	// the provider's own count, and the only one that is right across restarts.
	if refused := rateLimited("geoapify", resp); refused != nil {
		return Place{}, refused
	}
	if resp.StatusCode != http.StatusOK {
		return Place{}, fmt.Errorf("geocode: geoapify answered %s", resp.Status)
	}

	var payload struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return Place{}, fmt.Errorf("geocode: decode geoapify answer: %w", err)
	}
	if len(payload.Results) == 0 {
		return Place{}, fmt.Errorf("geocode: geoapify knows no place there")
	}

	// Flattened to the same shape Nominatim produces, so one label() builds
	// the name for both and two providers cannot drift into naming the same
	// cell two different ways.
	address := geoapifyAddress(payload.Results[0])
	return Place{
		Label:       label(address, precision, address["name"]),
		CountryCode: upper(address["country_code"]),
	}, nil
}

// geoapifyType asks for the detail the precision means.
//
// It is the counterpart of geo.NominatimZoom. Unlike doléances, where this
// stops at the town, every place here is a published one — the square a
// demonstration starts from — so a pin dropped at street level is asked about
// as a street, and one dropped on a building is asked about with no type at
// all, which is Geoapify's way of answering with the nearest address and the
// venue's own name.
//
// A district is asked about as a street too: Geoapify's `city` answer carries
// the city and not the quarter in it, and label() needs the quarter. The
// street that comes with it is not used at that precision.
func geoapifyType(precision uint) string {
	switch {
	case precision <= 2:
		return "country"
	case precision <= 3:
		return "state"
	case precision <= 4:
		return "county"
	case precision <= 5:
		return "city"
	case precision <= 10:
		return "street"
	default:
		return ""
	}
}

// geoapifyAddress renames Geoapify's fields to the ones label() reads.
//
// Both services describe the same places with different words, and the mapping
// is the whole of the difference between them: `city` is `city`, but a French
// arrondissement is `district` here and `city_district` there, and `suburb`
// means the same thing in both. Anything unmapped is dropped rather than
// guessed — a label built from a field nobody checked is a label nobody can
// predict. `street` and `housenumber` are Nominatim's `road` and
// `house_number`, which label() reads only past district level.
func geoapifyAddress(result map[string]any) map[string]string {
	text := func(key string) string {
		if value, ok := result[key].(string); ok {
			return value
		}
		return ""
	}

	address := map[string]string{}
	for geoapify, nominatim := range map[string]string{
		"country":       "country",
		"country_code":  "country_code",
		"state":         "state",
		"county":        "county",
		"city":          "city",
		"town":          "town",
		"village":       "village",
		"municipality":  "municipality",
		"suburb":        "suburb",
		"district":      "city_district",
		"borough":       "borough",
		"quarter":       "quarter",
		"neighbourhood": "neighbourhood",
		"postcode":      "postcode",
		"street":        "road",
		"housenumber":   "house_number",
		"name":          "name",
	} {
		if value := text(geoapify); value != "" {
			address[nominatim] = value
		}
	}
	return address
}
