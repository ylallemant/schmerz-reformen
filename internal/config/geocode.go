package config

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Geocode configuration keys.
const (
	KeyGeocodeEnabled  = "geocode-enabled"
	KeyGeocodeEndpoint = "geocode-endpoint"
	KeyGeocodeContact  = "geocode-contact"
)

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

	// Contact is an email address or URL put in the User-Agent, which
	// Nominatim's policy requires so an operator can be reached before being
	// blocked. It is the operator's address, never an editor's or a reader's.
	Contact string
}

// RegisterGeocodeFlags declares the geocoding flags. Only the backend performs
// lookups — deliberately, so no browser ever contacts a third party on this
// site's behalf.
func RegisterGeocodeFlags(cmd *cobra.Command) {
	f := cmd.PersistentFlags()
	f.Bool(KeyGeocodeEnabled, true, "name pinned locations by reverse geocoding")
	f.String(KeyGeocodeEndpoint, "https://nominatim.openstreetmap.org",
		"Nominatim instance; point this at your own for anything but a trial")
	f.String(KeyGeocodeContact, "",
		"operator contact put in the User-Agent, as Nominatim's usage policy requires")
}

// LoadGeocode reads the resolved geocoding configuration.
func LoadGeocode() Geocode {
	return Geocode{
		Enabled:  viper.GetBool(KeyGeocodeEnabled),
		Endpoint: viper.GetString(KeyGeocodeEndpoint),
		Contact:  viper.GetString(KeyGeocodeContact),
	}
}

// UserAgent renders the identification Nominatim requires. A request without
// one that names the application is refused, and an operator who set no
// contact still gets a truthful agent rather than a forged one.
func (g Geocode) UserAgent(version string) string {
	agent := "schmerz-reformen/" + version
	if g.Contact != "" {
		agent += " (" + g.Contact + ")"
	}
	return agent
}
