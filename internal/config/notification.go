package config

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// KeyNotificationLanguage is the language of the few words a notification
// needs that are not somebody's content.
const KeyNotificationLanguage = "notification-language"

// DefaultNotificationLanguage is German: the reforms this site is about are
// German ones, and so are most of the people being told about them.
const DefaultNotificationLanguage = "de"

// RegisterNotificationFlags declares how the backend words a notification.
//
// Almost everything in a notification is content an editor wrote — a
// collective's name, an update's title — and needs no translating. What is
// left is a word or two: "cancelled", "moved". The backend has no idea which
// language a reader's lock screen is in, because an account holds nothing
// about its owner, so those words are in one language for the installation.
func RegisterNotificationFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().String(KeyNotificationLanguage, DefaultNotificationLanguage,
		"language of the words the backend itself puts in a notification (de, en)")
}

// LoadNotificationLanguage reads it, lower-cased.
func LoadNotificationLanguage() string {
	language := strings.ToLower(strings.TrimSpace(viper.GetString(KeyNotificationLanguage)))
	if language == "" {
		return DefaultNotificationLanguage
	}
	return language
}
