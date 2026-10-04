package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ylallemant/schmerz-reformen/internal/models"
	"github.com/ylallemant/schmerz-reformen/internal/secret"
	"github.com/ylallemant/schmerz-reformen/internal/store/storetest"
)

// keyedStore opens a fresh database whose credentials are sealed with key.
// Two stores over one database are how a test reads it back with a different
// key, or with none.
func keyedStore(t *testing.T, driver, dsn, key string) *Store {
	t.Helper()
	box, err := secret.New(key)
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	s, err := Open(Options{Driver: Driver(driver), DSN: dsn, Secrets: box})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close(context.Background()) }) //nolint:errcheck
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

func newKey(t *testing.T) string {
	t.Helper()
	key, err := secret.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func provisioned() models.AuthSettings {
	return models.AuthSettings{
		Provisioned:   true,
		AppName:       "schmerz",
		InstanceURL:   "https://authentik.example",
		ConsoleURL:    "https://console.example",
		ClientID:      "a-client-id",
		ClientSecret:  "a-client-secret-nobody-else-may-have",
		APIToken:      "a-token-that-can-make-users",
		AdminGroup:    "uuid-admins",
		ProvisionedBy: "dominique",
	}
}

// TestNeitherSecretIsInTheDatabase. One lets somebody sign in as the console;
// the other lets them make users in the operator's own directory. Of
// everything this site stores, these are the two worth a backup being inert
// about.
func TestNeitherSecretIsInTheDatabase(t *testing.T) {
	driver, dsn := storetest.Database(t)
	s := keyedStore(t, driver, dsn, newKey(t))
	ctx := context.Background()

	if err := s.SaveAuthSettings(ctx, provisioned(), false); err != nil {
		t.Fatalf("SaveAuthSettings: %v", err)
	}

	var clientSecret, token string
	err := s.DB().Raw("select client_secret, api_token from auth_settings where id = ?",
		models.AuthSettingsID).Row().Scan(&clientSecret, &token)
	if err != nil {
		t.Fatal(err)
	}
	for name, stored := range map[string]string{"client secret": clientSecret, "api token": token} {
		if strings.Contains(stored, "nobody-else") || strings.Contains(stored, "make-users") ||
			!strings.HasPrefix(stored, "enc:v1:") {
			t.Errorf("the %s is stored as %q, want it sealed", name, stored)
		}
	}

	read, err := s.AuthSettings(ctx)
	if err != nil {
		t.Fatalf("AuthSettings: %v", err)
	}
	if read.ClientSecret != provisioned().ClientSecret || read.APIToken != provisioned().APIToken {
		t.Error("the secrets do not read back as they were written")
	}
}

// TestAProvisionedInstallationIsNotRepointedByAccident: the one write that can
// hand this site's administration to another directory needs saying twice.
func TestAProvisionedInstallationIsNotRepointedByAccident(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	if err := s.SaveAuthSettings(ctx, provisioned(), false); err != nil {
		t.Fatalf("first SaveAuthSettings: %v", err)
	}

	elsewhere := provisioned()
	elsewhere.InstanceURL = "https://somebody-else.example"
	if err := s.SaveAuthSettings(ctx, elsewhere, false); !errors.Is(err, ErrAlreadyProvisioned) {
		t.Fatalf("a second write without reprovision: err = %v, want ErrAlreadyProvisioned", err)
	}
	read, _ := s.AuthSettings(ctx)
	if read.InstanceURL != "https://authentik.example" {
		t.Errorf("instance = %q after a refused write", read.InstanceURL)
	}

	if err := s.SaveAuthSettings(ctx, elsewhere, true); err != nil {
		t.Fatalf("reprovision: %v", err)
	}
	read, _ = s.AuthSettings(ctx)
	if read.InstanceURL != "https://somebody-else.example" {
		t.Errorf("instance = %q after a deliberate reprovision", read.InstanceURL)
	}
}

// TestProvisionedIsAnswerableWithoutTheKey: a mistyped settings key must not
// make an installation believe it was never set up — that would reopen the
// setup door, the one thing a configuration mistake must never do.
func TestProvisionedIsAnswerableWithoutTheKey(t *testing.T) {
	driver, dsn := storetest.Database(t)
	written := keyedStore(t, driver, dsn, newKey(t))
	ctx := context.Background()
	if err := written.SaveAuthSettings(ctx, provisioned(), false); err != nil {
		t.Fatal(err)
	}

	wrongKey := keyedStore(t, driver, dsn, newKey(t))
	done, err := wrongKey.AuthProvisioned(ctx)
	if err != nil || !done {
		t.Errorf("AuthProvisioned with the wrong key = %v, %v; want true", done, err)
	}
	if _, err := wrongKey.AuthSettings(ctx); err == nil {
		t.Error("the secrets were read with the wrong key")
	}

	noKey := keyedStore(t, driver, dsn, "")
	if _, err := noKey.AuthSettings(ctx); !errors.Is(err, secret.ErrNoKey) {
		t.Errorf("reading sealed secrets with no key: err = %v, want ErrNoKey", err)
	}
}

func TestAFreshInstallationIsUnprovisioned(t *testing.T) {
	s := newStore(t)
	settings, err := s.AuthSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if settings.Provisioned || settings.HasToken() {
		t.Errorf("fresh settings = %+v", settings)
	}
	if done, _ := s.AuthProvisioned(context.Background()); done {
		t.Error("a fresh installation reports itself provisioned")
	}
}

func TestTheIssuerIsTheApplications(t *testing.T) {
	got := provisioned()
	got.InstanceURL = "https://auth.example/"
	if issuer := got.Issuer(); issuer != "https://auth.example/application/o/schmerz/" {
		t.Errorf("issuer = %q", issuer)
	}
	if name := got.AdminGroupName(); name != "schmerz-admins" {
		t.Errorf("admin group = %q, want the name this project used before provisioning", name)
	}
}
