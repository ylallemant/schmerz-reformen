package config

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Staff configuration keys.
//
// "Staff" is everybody who signs in to the console: the administrators of the
// installation and the content creators of each collective. They are identified
// by Authentik over OIDC, and what each may touch is decided by the groups the
// identity provider says they are in — never by a table of our own.
const (
	// KeyStaffToken is the secret the console presents to the backend.
	KeyStaffToken = "staff-token"

	// KeyAdminGroup names the identity-provider group whose members administer
	// the whole installation.
	KeyAdminGroup = "admin-group"

	// KeyConsoleURL is where the console answers, as an editor's browser sees it.
	KeyConsoleURL = "console-url"

	KeyOIDCIssuer       = "oidc-issuer"
	KeyOIDCClientID     = "oidc-client-id"
	KeyOIDCClientSecret = "oidc-client-secret"
	KeyOIDCGroupsClaim  = "oidc-groups-claim"

	// KeySessionSecret signs the console's own session cookie.
	KeySessionSecret = "session-secret"

	// KeyDevelopmentGroups are the groups the stand-in identity carries when
	// authentication is switched off.
	KeyDevelopmentGroups = "development-groups"
)

// DefaultAdminGroup is the group whose members administer the installation.
const DefaultAdminGroup = "schmerz-admins"

// defaultConsoleURL is a local development console.
const defaultConsoleURL = "http://localhost:8400"

// Staff is what the backend needs in order to believe the console.
type Staff struct {
	// Token is the shared secret. The backend is not publicly exposed, but
	// "not exposed" is a property of a deployment and this is a property of the
	// request: the frontend can reach the backend too, and a bug there must not
	// be able to speak as an editor.
	Token string

	// AdminGroup is the group that may do everything.
	AdminGroup string
}

// RegisterStaffFlags declares what the backend and the console share: one
// secret and the name of the administrators' group. The same keys on both, so
// one environment variable shapes the whole deployment.
func RegisterStaffFlags(cmd *cobra.Command) {
	f := cmd.PersistentFlags()
	f.String(KeyStaffToken, "",
		"secret the console presents to the backend; without it the content API refuses every call")
	f.String(KeyAdminGroup, DefaultAdminGroup,
		"identity-provider group whose members administer the whole installation")
}

// LoadStaff reads the shared staff configuration.
func LoadStaff() Staff {
	group := strings.TrimSpace(viper.GetString(KeyAdminGroup))
	if group == "" {
		group = DefaultAdminGroup
	}
	return Staff{
		Token:      strings.TrimSpace(viper.GetString(KeyStaffToken)),
		AdminGroup: group,
	}
}

// OIDC is how the console signs its editors in.
type OIDC struct {
	// Issuer is the provider's issuer URL — for Authentik,
	// https://auth.example.org/application/o/<application-slug>/.
	Issuer string

	ClientID     string
	ClientSecret string

	// RedirectURL is where the provider sends an editor back to. Derived from
	// the console's own URL rather than configured separately, because one
	// wrong answer in two places is two bugs.
	RedirectURL string

	// GroupsClaim is the claim listing an identity's groups. Authentik calls it
	// "groups" and sends it with the profile scope.
	GroupsClaim string

	// SessionSecret signs the session cookie. Empty means a random one is made
	// at startup, which signs everybody out on a restart and cannot work
	// behind more than one replica — the console says so loudly.
	SessionSecret string

	// ConsoleURL is the console's public origin, without a trailing slash.
	ConsoleURL string

	// DevelopmentGroups are carried by the stand-in identity when
	// authentication is off, so the group rules can be exercised locally.
	DevelopmentGroups []string
}

// RegisterOIDCFlags declares the console's sign-in flags. Only the console
// registers them: it is the only service an editor's browser ever reaches.
func RegisterOIDCFlags(cmd *cobra.Command) {
	f := cmd.PersistentFlags()
	f.String(KeyConsoleURL, defaultConsoleURL,
		"public URL editors reach the console on; the OIDC redirect is derived from it")
	f.String(KeyOIDCIssuer, "", "OIDC issuer URL of the identity provider")
	f.String(KeyOIDCClientID, "", "OIDC client id of the console")
	f.String(KeyOIDCClientSecret, "", "OIDC client secret of the console")
	f.String(KeyOIDCGroupsClaim, "groups", "claim that lists an identity's groups")
	f.String(KeySessionSecret, "",
		"secret signing the console session cookie; empty makes a random one per start")
	f.StringSlice(KeyDevelopmentGroups, nil,
		"DEVELOPMENT ONLY: groups of the stand-in identity (default: the admin group)")
}

// LoadOIDC reads the console's sign-in configuration.
//
// With development set, nothing about the provider is required: there is no
// provider. Without it every field is, and a missing one is refused here
// rather than at the first sign-in, where the error would appear in an
// editor's browser and nowhere else.
func LoadOIDC(development bool) (OIDC, error) {
	o := OIDC{
		Issuer:        strings.TrimSpace(viper.GetString(KeyOIDCIssuer)),
		ClientID:      strings.TrimSpace(viper.GetString(KeyOIDCClientID)),
		ClientSecret:  strings.TrimSpace(viper.GetString(KeyOIDCClientSecret)),
		GroupsClaim:   strings.TrimSpace(viper.GetString(KeyOIDCGroupsClaim)),
		SessionSecret: viper.GetString(KeySessionSecret),
		ConsoleURL:    strings.TrimRight(strings.TrimSpace(viper.GetString(KeyConsoleURL)), "/"),
	}
	if o.GroupsClaim == "" {
		o.GroupsClaim = "groups"
	}
	for _, group := range viper.GetStringSlice(KeyDevelopmentGroups) {
		if group = strings.TrimSpace(group); group != "" {
			o.DevelopmentGroups = append(o.DevelopmentGroups, group)
		}
	}

	parsed, err := url.Parse(o.ConsoleURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return OIDC{}, fmt.Errorf("invalid %s %q: want scheme://host", KeyConsoleURL, o.ConsoleURL)
	}
	o.RedirectURL = o.ConsoleURL + "/auth/callback"

	if development {
		return o, nil
	}
	for key, value := range map[string]string{
		KeyOIDCIssuer:       o.Issuer,
		KeyOIDCClientID:     o.ClientID,
		KeyOIDCClientSecret: o.ClientSecret,
	} {
		if value == "" {
			return OIDC{}, fmt.Errorf("%s is required unless --%s is set", key, KeyDevelopment)
		}
	}
	return o, nil
}
