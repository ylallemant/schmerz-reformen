package harness

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// settingsKeyFile is where a run keeps the key its backend seals the identity
// provider's credentials with.
const settingsKeyFile = "settings.key"

// SettingsKey returns the run's settings key, making one the first time.
//
// Kept in the run directory rather than made per launch: a resumed run has to
// read the credentials it sealed last time, and a fresh key would find a
// provisioned console it can no longer sign anybody in to.
func SettingsKey(dir RunDir) (string, error) {
	path := filepath.Join(dir.Root, settingsKeyFile)
	if raw, err := os.ReadFile(path); err == nil {
		return strings.TrimSpace(string(raw)), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read %s: %w", path, err)
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	if err := os.WriteFile(path, []byte(encoded+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return encoded, nil
}

// NewStaffToken makes the secret the console and the backend share for one
// launch. Nothing outlives the processes that hold it, so nothing keeps it.
func NewStaffToken() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
