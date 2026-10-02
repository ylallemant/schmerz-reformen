package config

import (
	"fmt"
	"strings"
	"time"

	// The zone database, compiled in. The runtime images are distroless and
	// must not depend on whether the base happens to ship one: a missing zone
	// would not fail, it would silently show every action in UTC — one or two
	// hours off the time on the flyer.
	_ "time/tzdata"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// KeyTimezone is the time zone dates and times are shown and entered in.
const KeyTimezone = "timezone"

// DefaultTimezone is where the reforms this site is about are being made.
const DefaultTimezone = "Europe/Berlin"

// RegisterTimezoneFlags declares the installation's time zone. The two web
// services register it: the console reads what an editor typed in it, and the
// frontend shows it back to a reader in it.
//
// **The installation's zone, never the reader's.** An action happens at a
// wall-clock time in a place. "14:00" on the page has to be the 14:00 on the
// flyer, for somebody reading from another country as much as for anybody
// else — a page that helpfully converted it would send them an hour late.
func RegisterTimezoneFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().String(KeyTimezone, DefaultTimezone,
		"IANA time zone dates and times are shown and entered in")
}

// LoadTimezone resolves the zone, refusing one that does not exist at startup
// rather than at the first page.
func LoadTimezone() (*time.Location, error) {
	name := strings.TrimSpace(viper.GetString(KeyTimezone))
	if name == "" {
		name = DefaultTimezone
	}
	zone, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("invalid %s %q: %w", KeyTimezone, name, err)
	}
	return zone, nil
}
