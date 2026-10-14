package config

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/ylallemant/schmerz-reformen/internal/geocode"
)

// Geocode configuration keys.
const (
	KeyGeocodeEnabled  = "geocode-enabled"
	KeyGeocodeEndpoint = "geocode-endpoint"
)

// KeyAPIKey names the setting that enables one geocoding provider.
//
// The per-provider settings are named from the provider itself rather than
// written out one by one: <name>-apikey, <name>-per-second and <name>-per-day,
// which the ordinary bootstrap reads from the environment as
// SCHMERZ_<NAME>_APIKEY and so on — the prefix is the binary name and the
// replacer turns each dash into an underscore.
//
// **The key is what enables a provider.** Supplying SCHMERZ_GEOAPIFY_APIKEY
// puts Geoapify in the queue; leaving it out leaves it out, and that is not an
// error. There is no second setting to agree with it — see geocode's registry
// for why, and for what adding a provider costs.
func KeyAPIKey(provider string) string { return provider + "-apikey" }

// KeyPerSecond and KeyPerDay name one provider's pace.
//
// Both are configuration because both are **bought**. Geoapify's free tier is
// 5 a second and 3,000 a day; every paid plan moves both, and a deployment
// that upgraded but could not say so would be paying for capacity this queue
// refuses to use. Nominatim's are the usage policy's when it is the public
// instance, and whatever the operator decides when they run their own — which
// is the usual reason to run one.
func KeyPerSecond(provider string) string { return provider + "-per-second" }

// KeyPerDay is the daily half of KeyPerSecond.
func KeyPerDay(provider string) string { return provider + "-per-day" }

// Geocode configures reverse geocoding — turning a pinned point into the name
// of a place.
type Geocode struct {
	// Enabled is on by default: without it a location is a dot with no name,
	// which is markedly worse for a site organised by place.
	Enabled bool

	// Endpoint is a Nominatim instance. The public one at
	// nominatim.openstreetmap.org has a usage policy that forbids heavy use,
	// so any deployment expecting traffic points this at its own.
	Endpoint string

	// Keys are the API keys of the providers that need one, by provider name,
	// and they are what decides which providers the queue has.
	//
	// A key that is absent means that provider is not used. That is not an
	// error: the queue simply has one fewer, and Nominatim answers everything
	// at its one request a second. Supplying one adds that provider's rate
	// **beside** Nominatim's rather than instead of it — see geocode.Service,
	// where the queue spans every provider and the rates add.
	//
	// They are credentials, so they come from the environment in any real
	// deployment: a key on a command line is visible in `ps` and in a
	// container spec.
	Keys map[string]string

	// Limits override each provider's defaults, keyed by provider name. A
	// zero in either field means "whatever that provider publishes", so a
	// deployment names only what its plan changed.
	Limits map[string]geocode.Limits
}

// RegisterGeocodeFlags declares the geocoding flags. Only the backend performs
// lookups — deliberately, so no browser ever contacts a third party on this
// site's behalf.
func RegisterGeocodeFlags(cmd *cobra.Command) {
	f := cmd.PersistentFlags()
	f.Bool(KeyGeocodeEnabled, true, "name pinned locations by reverse geocoding")
	f.String(KeyGeocodeEndpoint, "https://nominatim.openstreetmap.org",
		"Nominatim instance; point this at your own for anything but a trial")
	// Declared from geocode's own registry, so a provider added there arrives
	// here with its three settings and no edit to this file.
	for _, provider := range geocode.KeyedNames() {
		f.String(KeyAPIKey(provider), "", fmt.Sprintf(
			"%s API key; that provider is used only when this is set (SCHMERZ_%s_APIKEY)",
			provider, strings.ToUpper(strings.ReplaceAll(provider, "-", "_"))))
	}
	for _, provider := range geocode.Names() {
		published := geocode.Defaults(provider)
		f.Float64(KeyPerSecond(provider), 0, fmt.Sprintf(
			"%s requests a second; 0 keeps its published %g", provider, published.PerSecond))
		f.Int(KeyPerDay(provider), 0, fmt.Sprintf(
			"%s requests a day; 0 keeps its published %d", provider, published.PerDay))
	}
}

// LoadGeocode reads the resolved geocoding configuration.
func LoadGeocode() Geocode {
	return Geocode{
		Enabled:  viper.GetBool(KeyGeocodeEnabled),
		Endpoint: viper.GetString(KeyGeocodeEndpoint),

		Keys:   loadProviderKeys(),
		Limits: loadProviderLimits(),
	}
}

// loadProviderKeys reads the keys that were configured, and only those. A
// blank one is left out rather than stored, so "has a key" and "is used" are
// one fact for every reader of this struct.
func loadProviderKeys() map[string]string {
	keys := map[string]string{}
	for _, provider := range geocode.KeyedNames() {
		if apiKey := strings.TrimSpace(viper.GetString(KeyAPIKey(provider))); apiKey != "" {
			keys[provider] = apiKey
		}
	}
	return keys
}

// loadProviderLimits reads a pace for every provider, keyed or not. A zero in
// either field means that provider's own published limit, so a deployment
// names only what its plan changed.
func loadProviderLimits() map[string]geocode.Limits {
	limits := map[string]geocode.Limits{}
	for _, provider := range geocode.Names() {
		limits[provider] = geocode.Limits{
			PerSecond: viper.GetFloat64(KeyPerSecond(provider)),
			PerDay:    viper.GetInt(KeyPerDay(provider)),
		}
	}
	return limits
}
