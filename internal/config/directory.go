package config

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// The backend's access to the directory, when the deployment platform gives
// it one: the address of Authentik's API on this project's own sign-in
// address, and the token of the service account the platform made for it.
//
// Without them the backend uses what its setup stored — a token of the service
// account the setup made. With them that stored token is not used at all: the
// platform owns the account, sets its token back to its own value on every
// reconcile, and a token kept in the database would stop working the moment
// it did.
const (
	KeyAuthentikURL   = "authentik-url"
	KeyAuthentikToken = "authentik-token"
)

// DirectoryAccess is the platform's account for the backend.
type DirectoryAccess struct {
	URL   string
	Token string
}

// RegisterDirectoryFlags declares them. The backend's alone: the console never
// talks to the directory.
func RegisterDirectoryFlags(cmd *cobra.Command) {
	f := cmd.PersistentFlags()
	f.String(KeyAuthentikURL, "",
		"the directory's API as the deployment platform gives it; with --authentik-token, used instead of what setup stored")
	f.String(KeyAuthentikToken, "",
		"the token of the service account the deployment platform made for this backend")
}

// LoadDirectoryAccess reads them: nil when neither is set, refused when only
// one is — a backend with an address and no token would refuse every editor
// at the first request rather than here.
func LoadDirectoryAccess() (*DirectoryAccess, error) {
	access := DirectoryAccess{
		URL:   strings.TrimSpace(viper.GetString(KeyAuthentikURL)),
		Token: strings.TrimSpace(viper.GetString(KeyAuthentikToken)),
	}
	switch {
	case access.URL == "" && access.Token == "":
		return nil, nil //nolint:nilnil // the setup's own token is the ordinary case
	case access.URL == "" || access.Token == "":
		return nil, fmt.Errorf("%s and %s come together: one without the other reaches no directory",
			KeyAuthentikURL, KeyAuthentikToken)
	}
	parsed, err := url.Parse(access.URL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return nil, fmt.Errorf("%s must be an http or https URL, got %q", KeyAuthentikURL, access.URL)
	}
	return &access, nil
}
