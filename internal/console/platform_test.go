package console

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ylallemant/schmerz-reformen/internal/apiclient"
	"github.com/ylallemant/schmerz-reformen/internal/config"
)

// fakeIssuer answers OIDC discovery as Authentik does for one application.
func fakeIssuer(t *testing.T) string {
	t.Helper()
	var issuer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/application/o/schmerz/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"issuer":                 issuer,
			"authorization_endpoint": issuer + "authorize/",
			"token_endpoint":         issuer + "token/",
			"jwks_uri":               issuer + "jwks/",
		})
	}))
	t.Cleanup(server.Close)
	issuer = server.URL + "/application/o/schmerz/"
	return issuer
}

// TestThePlatformsClientSignsInWithoutTheBackend: with the deployment
// platform's client in its environment the console needs nothing from the
// backend to sign people in — the issuer, the id and the secret are its own,
// and the redirect is its own address.
func TestThePlatformsClientSignsInWithoutTheBackend(t *testing.T) {
	issuer := fakeIssuer(t)
	// A backend that answers nothing: asking it would fail the connection.
	backend, err := apiclient.New("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	c := &console{
		backend:    backend,
		consoleURL: "https://console.example",
		platform:   &config.PlatformClient{Issuer: issuer, ClientID: "schmerz", ClientSecret: "a-secret"},
	}

	if err := c.connectIdentityProvider(context.Background()); err != nil {
		t.Fatalf("connectIdentityProvider: %v", err)
	}
	signIn := c.currentSignIn()
	if signIn == nil {
		t.Fatal("no sign-in")
	}
	if signIn.oauth.ClientID != "schmerz" || signIn.oauth.ClientSecret != "a-secret" ||
		signIn.oauth.RedirectURL != "https://console.example"+CallbackPath ||
		signIn.oauth.Endpoint.AuthURL != issuer+"authorize/" {
		t.Errorf("sign-in = %+v", signIn.oauth)
	}
}

// TestABackendSetUpForThePlatformRefusesAConsoleWithoutItsClient: a console
// started without the platform's client in its environment would otherwise
// sign people in with an empty client id, against nothing.
func TestABackendSetUpForThePlatformRefusesAConsoleWithoutItsClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(apiclient.AuthSettings{Provisioned: true, SignInByPlatform: true}) //nolint:errcheck
	}))
	t.Cleanup(server.Close)
	backend, err := apiclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	c := &console{backend: backend, staffToken: "the-shared-secret"}

	err = c.connectIdentityProvider(context.Background())
	if err == nil || c.currentSignIn() != nil {
		t.Fatalf("connected anyway: %v", err)
	}
	// And says what to set: "the settings cannot be read" would send an
	// operator to the backend's log for a problem that is in this console's
	// environment.
	if !strings.Contains(err.Error(), config.KeyOIDCClientID) {
		t.Errorf("refused with %q, which does not name what to set", err)
	}
}
