package config

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// KeySiteURL is where the public site answers, and KeySiteName is what it
// calls itself.
const (
	KeySiteURL  = "site-url"
	KeySiteName = "site-name"
)

// RegisterSiteFlags declares where the frontend lives and what it is called.
//
// **A passkey is bound to this host for ever**, because the relying party id
// is derived from it and the private halves of those credentials live on
// people's devices where nothing can re-key them. See config.Auth.
//
// The name is what a password manager shows beside the entry when somebody
// picks a passkey, so it is the one string in this configuration that a person
// reads in software this project does not control. It is also the title of the
// feeds and the calendar a reader subscribes to.
func RegisterSiteFlags(cmd *cobra.Command) {
	f := cmd.PersistentFlags()
	f.String(KeySiteURL, defaultSiteURL,
		"public URL readers reach this site on — passkeys are bound to its host for ever")
	f.String(KeySiteName, defaultSiteName,
		"what this site calls itself, as a password manager and a feed reader show it")
}

// The defaults are a local development frontend, so a `go run ./test` instance
// has working accounts without being told anything.
const (
	defaultSiteURL  = "http://localhost:8401"
	defaultSiteName = "schMERZ-Reformen"
)

// LoadSiteURL reads it, without a trailing slash.
func LoadSiteURL() string {
	return strings.TrimRight(strings.TrimSpace(viper.GetString(KeySiteURL)), "/")
}

// LoadSiteName reads what the site calls itself.
func LoadSiteName() string {
	name := strings.TrimSpace(viper.GetString(KeySiteName))
	if name == "" {
		return defaultSiteName
	}
	return name
}
