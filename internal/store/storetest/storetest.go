// Package storetest gives a test an empty database of its own, on the engine
// the environment names.
//
// SQLite by default, so `go test ./...` needs nothing installed. With
// SCHMERZ_TEST_POSTGRES_DSN set, every test that goes through here runs
// against PostgreSQL instead — which is how CI checks that the schema and the
// queries mean the same thing on the engine production uses. Without that, a
// type only one engine has (`blob` was the one that got through) passes every
// test and fails at the first deployment.
//
// It deliberately does not import the store package, so the store's own
// internal tests can use it without an import cycle: it hands back a driver
// name and a connection string, and the caller opens them.
package storetest

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	// The driver behind database/sql for creating and dropping the schema.
	// It is the one GORM's postgres dialector uses too, so no second driver
	// enters the build.
	_ "github.com/jackc/pgx/v5/stdlib"
)

// PostgresEnv names the variable holding a PostgreSQL connection URL. The
// role it names must be allowed to create schemas in that database.
const PostgresEnv = "SCHMERZ_TEST_POSTGRES_DSN"

// Database returns the driver ("sqlite" or "postgres") and the connection
// string of an empty database that belongs to this test alone.
//
// On PostgreSQL that is a schema of its own, selected through search_path and
// dropped when the test ends — so tests may run in parallel against one
// server and leave nothing behind.
func Database(t testing.TB) (driver, dsn string) {
	t.Helper()

	base := os.Getenv(PostgresEnv)
	if base == "" {
		return "sqlite", filepath.Join(t.TempDir(), "test.db")
	}

	name := schemaName(t)

	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatalf("open %s: %v", PostgresEnv, err)
	}
	defer admin.Close() //nolint:errcheck

	if _, err := admin.Exec(`CREATE SCHEMA ` + name); err != nil {
		t.Fatalf("create test schema %s: %v", name, err)
	}

	// Registered before the caller registers its own Close, so it runs after
	// it: cleanups run last-in first-out, and a schema cannot be dropped
	// while a pool still holds connections into it comfortably.
	t.Cleanup(func() {
		cleanup, err := sql.Open("pgx", base)
		if err != nil {
			t.Errorf("reopen to drop %s: %v", name, err)
			return
		}
		defer cleanup.Close() //nolint:errcheck
		if _, err := cleanup.Exec(`DROP SCHEMA ` + name + ` CASCADE`); err != nil {
			t.Errorf("drop test schema %s: %v", name, err)
		}
	})

	return "postgres", withSearchPath(base, name)
}

// schemaName is unique per call and a valid unquoted identifier: lower case,
// starting with a letter, well inside the 63-byte limit.
func schemaName(t testing.TB) string {
	t.Helper()

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("read randomness: %v", err)
	}
	return "test_" + hex.EncodeToString(suffix)
}

// withSearchPath points a connection string at one schema. pgx sends a
// parameter it does not recognise to the server as a run-time setting, so
// this works for every connection the pool opens, not just the first.
func withSearchPath(dsn, schema string) string {
	if strings.Contains(dsn, "://") {
		parsed, err := url.Parse(dsn)
		if err == nil {
			query := parsed.Query()
			query.Set("search_path", schema)
			parsed.RawQuery = query.Encode()
			return parsed.String()
		}
	}
	// The keyword form: "host=… user=… dbname=…".
	return dsn + " search_path=" + schema
}
