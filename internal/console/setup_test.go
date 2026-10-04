package console

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ylallemant/schmerz-reformen/internal/apiclient"
	"github.com/ylallemant/schmerz-reformen/internal/staffauth"
	"github.com/ylallemant/schmerz-reformen/internal/web"
)

// setupBackend answers the console's own routes: whether it is provisioned,
// and the provisioning request, which it records.
type setupBackend struct {
	server *httptest.Server

	mu          sync.Mutex
	provisioned bool
	status      int // of the auth settings answer; 0 is 200
	sent        []apiclient.ProvisionRequest
	tokens      []string
}

func newSetupBackend(t *testing.T) *setupBackend {
	t.Helper()
	b := &setupBackend{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/console/auth", func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.tokens = append(b.tokens, r.Header.Get(staffauth.TokenHeader))
		if b.status != 0 {
			http.Error(w, `{"detail":"broken"}`, b.status)
			return
		}
		json.NewEncoder(w).Encode(apiclient.AuthSettings{Provisioned: b.provisioned}) //nolint:errcheck
	})
	mux.HandleFunc("POST /v1/console/auth/provision", func(w http.ResponseWriter, r *http.Request) {
		var request apiclient.ProvisionRequest
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &request) //nolint:errcheck
		b.mu.Lock()
		b.sent = append(b.sent, request)
		b.tokens = append(b.tokens, r.Header.Get(staffauth.TokenHeader))
		b.mu.Unlock()
		if request.Token == "refused" {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte(`{"detail":"the token was refused: authentik refused this console's API token"}`)) //nolint:errcheck
			return
		}
		json.NewEncoder(w).Encode(apiclient.Provisioning{ //nolint:errcheck
			Done: true, AdminUsername: request.AdminUsername, TokenOwner: "akadmin",
			InstanceURL: request.InstanceURL, AppName: request.AppName, RecoveryReady: true,
			OwnToken: "schmerz-console", AdminLink: "https://auth.example/if/flow/recovery/?token=first",
		})
	})
	b.server = httptest.NewServer(mux)
	t.Cleanup(b.server.Close)
	return b
}

func setupConsole(t *testing.T, backendURL string, door SetupDoor) *console {
	t.Helper()
	backend, err := apiclient.New(backendURL)
	if err != nil {
		t.Fatal(err)
	}
	return &console{
		backend: backend, staffToken: "the-shared-secret",
		authentikURL: "https://auth.example.org", consoleURL: "https://console.example",
		setupDoor: door,
	}
}

// deadlineRecorder is a ResponseWriter that remembers having its write
// deadline cleared — httptest's recorder does not implement it, which is how
// a test of a slow handler passes while the real server drops the connection.
type deadlineRecorder struct {
	*httptest.ResponseRecorder
	cleared bool
}

func (d *deadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	d.cleared = deadline.IsZero()
	return nil
}

// TestTheWizardOutlivesTheWriteDeadline: provisioning takes forty seconds
// against a remote Authentik and the server allows thirty. Without clearing
// the deadline, the answer carrying the first admin's only way in is written
// to a closed connection while everything behind it succeeds.
func TestTheWizardOutlivesTheWriteDeadline(t *testing.T) {
	c := setupConsole(t, newSetupBackend(t).server.URL, SetupDoor{Open: true})
	recorder := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	c.wizard(recorder, httptest.NewRequest(http.MethodGet, SetupPath, nil))
	if !recorder.cleared {
		t.Error("the wizard did not clear its write deadline")
	}
}

// TestTheWizardOffersWhatTheDeploymentSays: the Authentik address comes from
// SCHMERZ_OIDC_ISSUER (through --oidc-issuer), the console's from its own.
func TestTheWizardOffersWhatTheDeploymentSays(t *testing.T) {
	c := setupConsole(t, newSetupBackend(t).server.URL, SetupDoor{Open: true})
	recorder := httptest.NewRecorder()
	c.wizard(recorder, httptest.NewRequest(http.MethodGet, SetupPath, nil))
	body := recorder.Body.String()
	for _, want := range []string{
		`value="https://auth.example.org"`, `value="https://console.example"`, `value="schmerz"`,
		"https://console.example/auth/callback", "schmerz-admins",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the setup page does not offer %s", want)
		}
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Error("the setup page may be cached, and it can carry somebody's way in")
	}
}

// TestProvisioningIsTheBackendsAndTheDoorClosesBehindIt.
func TestProvisioningIsTheBackendsAndTheDoorClosesBehindIt(t *testing.T) {
	backend := newSetupBackend(t)
	c := setupConsole(t, backend.server.URL, SetupDoor{Open: true})

	form := url.Values{
		"instance_url": {"https://auth.example.org"}, "token": {"pasted-setup-token"},
		"app_name": {""}, "console_url": {""}, "admin_username": {"maria"}, "admin_name": {"Maria"},
	}
	request := httptest.NewRequest(http.MethodPost, SetupPath, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	c.wizard(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d\n%s", recorder.Code, recorder.Body.String())
	}
	backend.mu.Lock()
	sent := backend.sent[len(backend.sent)-1]
	token := backend.tokens[len(backend.tokens)-1]
	backend.mu.Unlock()
	if sent.AppName != "schmerz" || sent.ConsoleURL != "https://console.example" || sent.Reprovision {
		t.Errorf("sent %+v", sent)
	}
	if token != "the-shared-secret" {
		t.Errorf("the provisioning request went without the console's secret: %q", token)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "https://auth.example/if/flow/recovery/?token=first") ||
		!strings.Contains(body, "schmerz-console") {
		t.Error("the first admin's way in, or the token the backend kept, is not shown")
	}
	if strings.Contains(body, "pasted-setup-token") {
		t.Error("the pasted token was rendered back")
	}

	// Closed behind it.
	again := httptest.NewRecorder()
	c.wizard(again, httptest.NewRequest(http.MethodGet, SetupPath, nil))
	if again.Code != http.StatusNotFound {
		t.Errorf("the wizard after finishing = %d, want 404", again.Code)
	}
}

// TestAProvisioningRefusalSaysWhy, in Authentik's words, with the form kept.
func TestAProvisioningRefusalSaysWhy(t *testing.T) {
	c := setupConsole(t, newSetupBackend(t).server.URL, SetupDoor{Open: true, Reprovision: true})
	form := url.Values{"instance_url": {"https://auth.example.org"}, "token": {"refused"},
		"console_url": {"https://console.example"}, "admin_username": {"maria"}}
	request := httptest.NewRequest(http.MethodPost, SetupPath, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	c.wizard(recorder, request)

	body := recorder.Body.String()
	if recorder.Code != http.StatusBadGateway || !strings.Contains(body, "authentik refused this console") {
		t.Errorf("status %d, body without the reason", recorder.Code)
	}
	if !strings.Contains(body, `value="maria"`) || !strings.Contains(body, "already has one") {
		t.Error("the refused form lost what was typed, or the replacement warning")
	}
}

// TestTheDoorIsDecidedOnceAndShutWhenInDoubt.
func TestTheDoorIsDecidedOnceAndShutWhenInDoubt(t *testing.T) {
	backend := newSetupBackend(t)
	client, _ := apiclient.New(backend.server.URL)
	ctx := context.Background()

	if door := decideSetupDoor(ctx, client, false); !door.Open || door.Reprovision {
		t.Errorf("unprovisioned: %+v, want open", door)
	}
	backend.mu.Lock()
	backend.provisioned = true
	backend.mu.Unlock()
	if door := decideSetupDoor(ctx, client, false); door.Open {
		t.Errorf("provisioned: %+v, want shut", door)
	}
	if door := decideSetupDoor(ctx, client, true); !door.Open || !door.Reprovision {
		t.Errorf("provisioned with --superuser: %+v, want open to replace", door)
	}

	// A backend that answers badly is up and broken: no waiting, and shut.
	backend.mu.Lock()
	backend.status = http.StatusInternalServerError
	backend.mu.Unlock()
	start := time.Now()
	if door := decideSetupDoor(ctx, client, false); door.Open {
		t.Error("a broken backend opened the door")
	}
	if time.Since(start) > 2*time.Second {
		t.Error("a backend that answered was waited on as if it were starting")
	}
}

// TestOnlyAConnectionThatCouldNotBeMadeIsWaitedOn.
func TestOnlyAConnectionThatCouldNotBeMadeIsWaitedOn(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close() //nolint:errcheck

	client, _ := apiclient.New("http://" + address)
	_, err = client.AuthSettings(context.Background())
	if !starting(err) {
		t.Errorf("a closed port: starting(%v) = false", err)
	}
	if starting(errors.New("the backend answered 500")) {
		t.Error("an answer was mistaken for a backend that is not up yet")
	}
}

// TestTheWizardIsNotThereWhenTheDoorIsShut — absent, not refusing.
func TestTheWizardIsNotThereWhenTheDoorIsShut(t *testing.T) {
	shut := http.NewServeMux()
	setupConsole(t, "http://127.0.0.1:1", SetupDoor{}).registerSetup(shut)
	if _, pattern := shut.Handler(httptest.NewRequest(http.MethodGet, SetupPath, nil)); pattern != "" {
		t.Errorf("the wizard is registered on a shut door: %q", pattern)
	}

	open := http.NewServeMux()
	setupConsole(t, newSetupBackend(t).server.URL, SetupDoor{Open: true}).registerSetup(open)
	recorder := httptest.NewRecorder()
	open.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, SetupPath, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("the open wizard = %d", recorder.Code)
	}
	// The maintenance port has no service middleware, so the page carries
	// its own policy — or it would be the one HTML page without one.
	if policy := recorder.Header().Get("Content-Security-Policy"); !strings.Contains(policy, "default-src") {
		t.Errorf("the setup page has no Content-Security-Policy: %q", policy)
	}
	styles := httptest.NewRecorder()
	open.ServeHTTP(styles, httptest.NewRequest(http.MethodGet, SetupPath+"/setup.css", nil))
	if styles.Code != http.StatusOK || !strings.Contains(styles.Header().Get("Content-Type"), "text/css") {
		t.Errorf("the setup stylesheet = %d %q", styles.Code, styles.Header().Get("Content-Type"))
	}
}

// TestAConsoleWithNoIdentityProviderIsLocked: one that cannot authenticate
// anybody must not therefore let everybody in.
func TestAConsoleWithNoIdentityProviderIsLocked(t *testing.T) {
	locked := lockedConsole(t)

	for _, path := range []string{"/", "/collectives/c1", "/auth/login#de", "/settings/people"} {
		response, body := get(t, locked, path)
		if response.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("%s = %d, want 503", path, response.StatusCode)
		}
		if !strings.Contains(body, "/setup") {
			t.Errorf("%s does not say where the setup is", path)
		}
	}
	// What the locked page itself needs is still served.
	if response, _ := get(t, locked, "/static/base.css"); response.StatusCode != http.StatusOK {
		t.Errorf("the stylesheet of the locked page = %d", response.StatusCode)
	}
}

// lockedConsole rebuilds the console the way production starts one that has
// no identity provider: not in development, and with no sign-in.
func lockedConsole(t *testing.T) http.Handler {
	t.Helper()
	backend, _ := fakeBackend(t, false)
	c := testConsole(t, backend.URL)
	c.development = false
	mux := http.NewServeMux()
	if err := c.registerRoutes(mux); err != nil {
		t.Fatal(err)
	}
	return web.SecurityHeaders()(c.requireSignIn(mux))
}

// TestTheCallbackIsTheOneTheBackendRegisters: the console serves it, the
// backend writes it into the provider, Authentik matches it strictly.
func TestTheCallbackIsTheOneTheBackendRegisters(t *testing.T) {
	if CallbackPath != staffauth.CallbackPath {
		t.Errorf("console callback %q, registered %q", CallbackPath, staffauth.CallbackPath)
	}
	backend, _ := fakeBackend(t, false)
	c := testConsole(t, backend.URL)
	mux := http.NewServeMux()
	if err := c.registerRoutes(mux); err != nil {
		t.Fatal(err)
	}
	if _, pattern := mux.Handler(httptest.NewRequest(http.MethodGet, staffauth.CallbackPath, nil)); !strings.HasSuffix(pattern, staffauth.CallbackPath) {
		t.Errorf("nothing is served at the registered callback: pattern %q", pattern)
	}
}
