package storetest

import (
	"regexp"
	"strings"
	"testing"
)

func TestWithSearchPath(t *testing.T) {
	tests := []struct {
		name, dsn, want string
	}{
		{"url without a query", "postgres://u:p@db:5432/app",
			"postgres://u:p@db:5432/app?search_path=test_x"},
		{"url keeps its parameters", "postgres://u:p@db/app?sslmode=disable",
			"postgres://u:p@db/app?search_path=test_x&sslmode=disable"},
		{"url replaces a search_path", "postgres://db/app?search_path=public",
			"postgres://db/app?search_path=test_x"},
		{"keyword form", "host=db user=u dbname=app",
			"host=db user=u dbname=app search_path=test_x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := withSearchPath(tt.dsn, "test_x"); got != tt.want {
				t.Errorf("withSearchPath(%q) = %q, want %q", tt.dsn, got, tt.want)
			}
		})
	}
}

func TestSchemaNameIsAnUnquotedIdentifier(t *testing.T) {
	identifier := regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	first, second := schemaName(t), schemaName(t)
	for _, name := range []string{first, second} {
		if !identifier.MatchString(name) || !strings.HasPrefix(name, "test_") {
			t.Errorf("schema name %q is not a safe unquoted identifier", name)
		}
	}
	if first == second {
		t.Errorf("two calls returned the same schema %q", first)
	}
}

func TestDatabaseDefaultsToSQLite(t *testing.T) {
	t.Setenv(PostgresEnv, "")
	driver, dsn := Database(t)
	if driver != "sqlite" || !strings.HasSuffix(dsn, "test.db") {
		t.Errorf("Database() = %q, %q; want sqlite in a temporary directory", driver, dsn)
	}
}
