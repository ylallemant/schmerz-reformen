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

	// KeyOIDCIssuer is where the identity provider is. Since the console
	// provisions itself, it is only what the setup wizard offers as the
	// Authentik address — an issuer URL is accepted and cut back to the
	// instance, so SCHMERZ_OIDC_ISSUER can hold either.
	KeyOIDCIssuer = "oidc-issuer"

	// KeySuperuser reopens the setup wizard on a console that is already
	// configured. See RegisterOIDCFlags.
	KeySuperuser = "superuser"

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
//
// The client itself — its identifier and secret — is not here: the setup
// wizard provisions it in Authentik and the backend keeps it, so there is no
// way to configure a console that the directory knows nothing about.
type OIDC struct {
	// AuthentikURL is what the setup wizard offers as the Authentik instance:
	// --oidc-issuer, cut back to the instance when it is an issuer URL.
	AuthentikURL string

	// SessionSecret signs the session cookie. Empty means a random one is made
	// at startup, which signs everybody out on a restart and cannot work
	// behind more than one replica — the console says so loudly.
	SessionSecret string

	// ConsoleURL is the console's public origin, without a trailing slash. It
	// is what the wizard registers the redirect under.
	ConsoleURL string

	// Superuser reopens the setup wizard on a console that already has an
	// identity provider.
	Superuser bool

	// DevelopmentGroups are carried by the stand-in identity when
	// authentication is off, so the group rules can be exercised locally.
	DevelopmentGroups []string
}

// RegisterOIDCFlags declares the console's sign-in flags. Only the console
// registers them: it is the only service an editor's browser ever reaches.
//
// # --superuser is not --development
//
// They look alike and are opposites in the way that matters. --development
// switches authentication off, for local work on a console nobody relies on.
// --superuser switches nothing off: it puts the setup wizard back on the
// maintenance port of a console that is otherwise running normally, so an
// operator can point it at a different directory. Folding them together would
// mean "the identity provider moved" implied "first let everybody in".
func RegisterOIDCFlags(cmd *cobra.Command) {
	f := cmd.PersistentFlags()
	f.String(KeyConsoleURL, defaultConsoleURL,
		"public URL editors reach the console on; the OIDC redirect is registered under it")
	f.String(KeyOIDCIssuer, "",
		"the Authentik instance (or an issuer URL on it) the setup wizard offers")
	f.Bool(KeySuperuser, false,
		"reopen the setup wizard on the maintenance port of a console that is already configured")
	f.String(KeySessionSecret, "",
		"secret signing the console session cookie; empty makes a random one per start")
	f.StringSlice(KeyDevelopmentGroups, nil,
		"DEVELOPMENT ONLY: groups of the stand-in identity (default: the admin group)")
}

// LoadOIDC reads the console's sign-in configuration.
func LoadOIDC() (OIDC, error) {
	o := OIDC{
		AuthentikURL:  AuthentikInstance(viper.GetString(KeyOIDCIssuer)),
		SessionSecret: viper.GetString(KeySessionSecret),
		ConsoleURL:    strings.TrimRight(strings.TrimSpace(viper.GetString(KeyConsoleURL)), "/"),
		Superuser:     viper.GetBool(KeySuperuser),
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
	return o, nil
}

// AuthentikInstance reads an Authentik address out of what somebody put in
// --oidc-issuer: the instance itself, or an application's issuer URL on it —
// https://auth.example.org/application/o/<slug>/ — which is what that setting
// held before the console provisioned itself. Either way the wizard wants the
// instance.
func AuthentikInstance(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if at := strings.Index(value, "/application/o/"); at >= 0 {
		value = value[:at]
	}
	return strings.TrimRight(value, "/")
}
