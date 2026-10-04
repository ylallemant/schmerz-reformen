package models

import "time"

// AuthSettingsID is the fixed identifier of the single settings row.
const AuthSettingsID = "auth"

// DefaultAppName is what this site calls itself in somebody else's directory,
// unless the operator says otherwise.
const DefaultAppName = "schmerz"

// AdminGroupSuffix completes the administrators' group name.
//
// **The name it builds is a contract, not a detail.** It is what Authentik
// stores, what the backend compares against to decide who administers, and
// what somebody reading the audit log has to recognise. With the default
// application name it is `schmerz-admins` — the name this project used before
// the console could provision itself, so an installation configured by hand
// keeps working when it is provisioned.
//
// The prefix exists because one Authentik instance may hold more than one of
// these sites, or a test alongside the real thing. Without it the second
// installation would find the first one's group already there, join it, and
// quietly give its administrators power over somebody else's site.
const AdminGroupSuffix = "-admins"

// AdminGroupName builds the administrators' group name from the application
// name.
func AdminGroupName(app string) string { return app + AdminGroupSuffix }

// ValidAppName reports whether a name is safe to use as a slug and as a group
// name prefix.
//
// Narrow on purpose: it becomes an Authentik application slug, a group name
// and part of an OIDC issuer URL, and a name that was legal in one of those
// and not the others would fail somewhere far from where it was typed.
func ValidAppName(name string) bool {
	if name == "" || len(name) > 48 {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case (r == '-' || r == '_') && i > 0 && i < len(name)-1:
		default:
			return false
		}
	}
	return true
}

// AuthSettings is how the console reaches the identity provider its editors
// sign in through, as the setup wizard provisioned it.
//
// # Why this lives in the backend
//
// The console holds no database, and the backend owns every write. So the
// settings the console's own sign-in depends on live here — and so does the
// directory token, which the console never holds at all: everything that
// reads or changes the directory is a backend endpoint.
//
// # Both secrets are encrypted at rest
//
// ClientSecret and APIToken go through internal/secret on the way in and out
// when a settings key is configured. The API token is the more dangerous of
// the two by some distance: it can create people in the operator's directory.
type AuthSettings struct {
	Model

	// Provisioned says the wizard has run to completion. It is what closes the
	// setup door: a provisioned console does not register the wizard at all
	// unless it is started with --superuser.
	Provisioned bool `json:"provisioned"`

	// AppName is what this site is called in the directory: the application's
	// slug, and the prefix of the administrators' group. Stored because it
	// cannot be recomputed.
	AppName string `gorm:"size:64" json:"app_name,omitempty"`

	// InstanceURL is the Authentik instance, without the API path.
	InstanceURL string `gorm:"size:512" json:"instance_url,omitempty"`

	// ConsoleURL is where editors reach the console. Stored rather than
	// derived because Authentik matches a redirect URI strictly: the one
	// registered at provisioning and the one sent at every later sign-in have
	// to be the same string.
	ConsoleURL string `gorm:"size:512" json:"console_url,omitempty"`

	// ClientID and ClientSecret are the OAuth2 credentials of the provider the
	// wizard created. The secret goes to the console and to nothing else.
	ClientID     string `gorm:"size:256" json:"client_id,omitempty"`
	ClientSecret string `gorm:"size:1024" json:"-"`

	// APIToken is what the backend reads roles and manages people with. It
	// never leaves the backend.
	APIToken string `gorm:"size:1024" json:"-"`

	// AdminGroup is the identifier of the administrators' group, kept so
	// membership can be changed without looking it up by name.
	AdminGroup string `gorm:"size:64" json:"admin_group,omitempty"`

	// ProvisionedAt and ProvisionedBy record who opened the door and when.
	// The wizard runs before anybody can be identified by OIDC, so it cannot
	// be audited the way every later change is; the Authentik account whose
	// token was used is the nearest thing to a name.
	ProvisionedAt *time.Time `json:"provisioned_at,omitempty"`
	ProvisionedBy string     `gorm:"size:256" json:"provisioned_by,omitempty"`
}

// DefaultAuthSettings is what a fresh installation starts from: nothing
// configured, and a wizard waiting on the console's maintenance port.
func DefaultAuthSettings() AuthSettings {
	return AuthSettings{Model: Model{ID: AuthSettingsID}}
}

// HasToken reports whether a directory token is stored.
func (a AuthSettings) HasToken() bool { return a.APIToken != "" }

// AdminGroupName is the administrators' group of a provisioned installation.
func (a AuthSettings) AdminGroupName() string {
	if a.AppName == "" {
		return ""
	}
	return AdminGroupName(a.AppName)
}

// Issuer is the OIDC issuer of the provisioned application: Authentik scopes a
// discovery document to one application under its slug.
func (a AuthSettings) Issuer() string {
	if a.InstanceURL == "" || a.AppName == "" {
		return ""
	}
	return trimSlash(a.InstanceURL) + "/application/o/" + a.AppName + "/"
}

func trimSlash(value string) string {
	for len(value) > 0 && value[len(value)-1] == '/' {
		value = value[:len(value)-1]
	}
	return value
}
