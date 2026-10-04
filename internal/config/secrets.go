package config

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// KeySettingsKey seals the credentials the backend stores in its database:
// the identity provider's client secret and its directory token.
const KeySettingsKey = "settings-key"

// RegisterSecretFlags declares the backend's settings key.
//
// Optional, like the VAPID keys: without one the credentials are stored in
// clear and the backend says so at startup. With one, a database backup
// carries neither in a form anybody can use — as long as the key is not
// backed up beside it. Thirty-two random bytes, base64:
//
//	openssl rand -base64 32
func RegisterSecretFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().String(KeySettingsKey, "",
		"base64 key (32 bytes) sealing the stored identity-provider credentials; make one with `openssl rand -base64 32`")
}

// LoadSettingsKey reads it.
func LoadSettingsKey() string { return strings.TrimSpace(viper.GetString(KeySettingsKey)) }
