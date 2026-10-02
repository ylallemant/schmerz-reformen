package config

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Database configuration keys.
const (
	KeyDatabaseDriver   = "database-driver"
	KeyDatabaseDSN      = "database-dsn"
	KeyDatabaseMaxConns = "database-max-conns"
)

// Database configures the connection to the store.
type Database struct {
	// Driver is "postgres" in production, "sqlite" in development.
	Driver string

	// DSN is a PostgreSQL URL or a SQLite file path.
	DSN string

	MaxConns int
}

// RegisterDatabaseFlags declares the database flags. Only services that own
// data register them — the console and the frontend reach the database through
// the backend's API, never directly.
func RegisterDatabaseFlags(cmd *cobra.Command) {
	f := cmd.PersistentFlags()
	f.String(KeyDatabaseDriver, "sqlite", "database engine (postgres, sqlite)")
	f.String(KeyDatabaseDSN, "schmerz-reformen.db", "postgres connection URL, or sqlite file path")
	f.Int(KeyDatabaseMaxConns, 25, "maximum open database connections")
}

// LoadDatabase reads the resolved database configuration.
func LoadDatabase() (Database, error) {
	d := Database{
		Driver:   viper.GetString(KeyDatabaseDriver),
		DSN:      viper.GetString(KeyDatabaseDSN),
		MaxConns: viper.GetInt(KeyDatabaseMaxConns),
	}
	return d, d.Validate()
}

// Validate rejects a database configuration that cannot work.
func (d Database) Validate() error {
	switch d.Driver {
	case "postgres", "sqlite":
	default:
		return fmt.Errorf("invalid %s %q: want postgres or sqlite", KeyDatabaseDriver, d.Driver)
	}
	if d.DSN == "" {
		return fmt.Errorf("%s is required", KeyDatabaseDSN)
	}
	if d.MaxConns < 1 {
		return fmt.Errorf("invalid %s %d: must be at least 1", KeyDatabaseMaxConns, d.MaxConns)
	}
	return nil
}
