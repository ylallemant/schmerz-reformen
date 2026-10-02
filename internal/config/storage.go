package config

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Storage configuration keys.
//
// The role is "storage", never the technology behind it: S3 is one possible
// backend, and naming the code after it would weld a deployment decision into
// the source.
const (
	KeyStorageURL = "storage-url"
)

// Storage configures where uploaded media — the logos of collectives and of
// their member organisations — are kept.
type Storage struct {
	// URL is a gocloud.dev/blob URL: file:///var/lib/schmerz-reformen/storage
	// or s3://bucket?region=… (any S3-compatible provider). The scheme selects
	// the driver, so the backing service is configuration rather than code.
	URL string
}

// RegisterStorageFlags declares the storage flags. Only the backend owns
// storage, so only the backend registers these.
func RegisterStorageFlags(cmd *cobra.Command) {
	f := cmd.PersistentFlags()
	f.String(KeyStorageURL, "file://./storage", "storage location as a URL (file:// or s3://)")
}

// LoadStorage reads the resolved storage configuration.
func LoadStorage() (Storage, error) {
	s := Storage{URL: viper.GetString(KeyStorageURL)}
	return s, s.Validate()
}

// Validate rejects a configuration that would fail later, at the moment
// somebody uploads a logo.
func (s Storage) Validate() error {
	if strings.TrimSpace(s.URL) == "" {
		return fmt.Errorf("%s is required", KeyStorageURL)
	}
	if !strings.Contains(s.URL, "://") {
		return fmt.Errorf("invalid %s %q: want a URL such as file://./storage", KeyStorageURL, s.URL)
	}
	return nil
}
