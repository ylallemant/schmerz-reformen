package config

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/ylallemant/schmerz-reformen/internal/outbound"
)

// KeyOperatorContact is how whoever runs this installation can be reached by
// the services it calls.
const KeyOperatorContact = "operator-contact"

// DefaultOperatorContact is the installation's operators'. A deployment run
// by somebody else sets its own: this is the address a tile server, a
// geocoder or a push service writes to before blocking us.
const DefaultOperatorContact = "buero@die-linke-duesseldorf.de"

// Outbound is how the backend presents itself to the services it calls.
// See internal/outbound.
type Outbound struct {
	// Contact is put in the User-Agent of every outbound request: Nominatim's
	// usage policy requires one so an operator can be reached before being
	// blocked, and every other service is better for having it. It is the
	// operator's address, never an editor's or a reader's.
	Contact string
}

// RegisterOutboundFlags declares the contact.
func RegisterOutboundFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().String(KeyOperatorContact, DefaultOperatorContact,
		"how the services this backend calls can reach its operator; put in every outbound User-Agent")
}

// LoadOutbound reads it.
func LoadOutbound() Outbound {
	return Outbound{Contact: strings.TrimSpace(viper.GetString(KeyOperatorContact))}
}

// UserAgent is what every outbound request is made as.
func (o Outbound) UserAgent(version string) string {
	return outbound.UserAgent(version, DefaultProjectURL, o.Contact)
}
