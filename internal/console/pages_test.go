package console

import (
	"encoding/json"
	"io"
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

// hostile is what every text field of the fake backend's answers carries. An
// editor reads what other editors typed, and a script that ran here would run
// as somebody who can publish.
const hostile = `<script>alert('x')</script><img src=x onerror=alert(1)>" onmouseover="alert(2)`

const marker = "onmouseover"

// recorded is what the fake backend was sent.
type recorded struct {
	mu       sync.Mutex
	requests []received
}

type received struct {
	Method   string
	Path     string
	Body     map[string]any
	Identity staffauth.Identity
	Token    string
}

func (r *recorded) add(request received) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, request)
}

// last returns the most recent write to a path.
func (r *recorded) last(method, path string) (received, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.requests) - 1; i >= 0; i-- {
		if r.requests[i].Method == method && r.requests[i].Path == path {
			return r.requests[i], true
		}
	}
	return received{}, false
}

// fakeBackend answers the console's routes, refusing any call that does not
// carry the console's secret and somebody's identity — which is how these
// tests notice a handler that forgot to speak for the editor.
func fakeBackend(t *testing.T, refuse bool) (*httptest.Server, *recorded) {
	t.Helper()
	log := &recorded{}
	when := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

	collective := apiclient.Collective{
		ID: "c1", Name: hostile, Slug: "buendnis", Summary: hostile, Description: hostile,
		Website: "https://example.org/", Contact: hostile, Status: "published", AuthGroup: hostile,
		Place: hostile, Latitude: 51.2277, Longitude: 6.7735, LogoID: "logo-1",
		Members: []apiclient.Member{{ID: "m1", Name: hostile, Kind: "union", Position: 1}},
	}
	topic := apiclient.Topic{
		ID: "t1", CollectiveID: "c1", Kind: "cut", Level: "municipal",
		Title: hostile, Summary: hostile, Body: hostile, Amount: 100_000_000,
		Place: hostile, Latitude: 51.2277, Longitude: 6.7735, Status: "published", PublishedAt: &when,
	}
	update := apiclient.Update{
		ID: "u1", TopicID: "t1", CollectiveID: "c1", Title: hostile, Body: hostile,
		Excerpt: hostile, Status: "published", PublishedAt: &when,
	}
	action := apiclient.Action{
		ID: "a1", CollectiveID: "c1", TopicID: "t1", Kind: "demonstration", Title: hostile,
		Description: hostile, StartsAt: when, Place: hostile, Latitude: 51.2259, Longitude: 6.7724,
		Status: "published", Participants: 41,
	}

	answer := func(w http.ResponseWriter, body any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(body) //nolint:errcheck
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/staff/me", func(w http.ResponseWriter, _ *http.Request) {
		answer(w, apiclient.StaffProfile{Subject: "development", Admin: true,
			Collectives: []apiclient.Collective{collective}})
	})
	mux.HandleFunc("GET /v1/staff/collectives/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "c1" {
			http.Error(w, `{"detail":"no such collective"}`, http.StatusNotFound)
			return
		}
		answer(w, collective)
	})
	mux.HandleFunc("GET /v1/staff/collectives/{id}/topics", func(w http.ResponseWriter, _ *http.Request) {
		answer(w, map[string]any{"topics": []apiclient.Topic{topic}})
	})
	mux.HandleFunc("GET /v1/staff/topics/{id}", func(w http.ResponseWriter, _ *http.Request) { answer(w, topic) })
	mux.HandleFunc("GET /v1/staff/topics/{id}/updates", func(w http.ResponseWriter, _ *http.Request) {
		answer(w, map[string]any{"updates": []apiclient.Update{update}})
	})
	mux.HandleFunc("GET /v1/staff/updates/{id}", func(w http.ResponseWriter, _ *http.Request) { answer(w, update) })
	mux.HandleFunc("GET /v1/staff/collectives/{id}/actions", func(w http.ResponseWriter, _ *http.Request) {
		answer(w, map[string]any{"actions": []apiclient.Action{action}})
	})
	mux.HandleFunc("GET /v1/staff/actions/{id}", func(w http.ResponseWriter, _ *http.Request) { answer(w, action) })
	mux.HandleFunc("GET /v1/staff/audit", func(w http.ResponseWriter, _ *http.Request) {
		answer(w, map[string]any{"total": 120, "entries": []apiclient.AuditEntry{{
			ID: "e1", At: when, Actor: hostile, ActorName: hostile, Action: "publish",
			SubjectType: "topic", SubjectID: "t1", Collective: hostile, Summary: hostile,
		}}})
	})

	// Every write: recorded, then answered or refused.
	write := func(body any) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			if refuse {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusUnprocessableEntity)
				w.Write([]byte(`{"detail":"the title is too long: at most 160 characters"}`)) //nolint:errcheck
				return
			}
			answer(w, body)
		}
	}
	mux.HandleFunc("POST /v1/staff/collectives", write(collective))
	mux.HandleFunc("PUT /v1/staff/collectives/{id}", write(collective))
	mux.HandleFunc("POST /v1/staff/collectives/{id}/topics", write(topic))
	mux.HandleFunc("PUT /v1/staff/topics/{id}", write(topic))
	mux.HandleFunc("POST /v1/staff/topics/{id}/updates", write(update))
	mux.HandleFunc("PUT /v1/staff/updates/{id}", write(update))
	mux.HandleFunc("POST /v1/staff/collectives/{id}/actions", write(action))
	mux.HandleFunc("PUT /v1/staff/actions/{id}", write(action))
	mux.HandleFunc("POST /v1/staff/collectives/{id}/members", write(collective.Members[0]))
	mux.HandleFunc("DELETE /v1/staff/topics/{id}", write(map[string]bool{"done": true}))
	mux.HandleFunc("DELETE /v1/staff/collectives/{id}", write(map[string]bool{"done": true}))

	guarded := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, err := staffauth.Decode(r.Header.Get(staffauth.IdentityHeader))
		token := r.Header.Get(staffauth.TokenHeader)
		if err != nil || token != "the-shared-secret" {
			http.Error(w, `{"detail":"this route belongs to the console"}`, http.StatusUnauthorized)
			return
		}

		entry := received{Method: r.Method, Path: r.URL.Path, Identity: identity, Token: token}
		if r.Method != http.MethodGet {
			raw, _ := io.ReadAll(r.Body)
			json.Unmarshal(raw, &entry.Body) //nolint:errcheck // an empty body is simply nil
			log.add(entry)
		}
		mux.ServeHTTP(w, r)
	})

	server := httptest.NewServer(guarded)
	t.Cleanup(server.Close)
	return server, log
}

// served builds the whole console on a router of its own, signed in as the
// development identity.
func served(t *testing.T, backendURL string, groups ...string) http.Handler {
	t.Helper()

	backend, err := apiclient.New(backendURL)
	if err != nil {
		t.Fatalf("apiclient.New: %v", err)
	}
	renderer, err := web.NewRenderer(assets, "templates/shared/*.html", "templates/pages/*.html")
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	localization, err := web.NewLocalization(assets, "locales", fallbackLanguage)
	if err != nil {
		t.Fatalf("NewLocalization: %v", err)
	}
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}
	if len(groups) == 0 {
		groups = []string{"schmerz-admins"}
	}

	c := &console{
		assetVersion: "test",
		renderer:     renderer,
		localization: localization,
		backend:      backend,
		overlay:      web.NewThemeOverlay(backend),
		assets:       web.NewThemeAssetHandler(backend),
		files:        web.NewThemeFileHandler(backend),
		siteURL:      "https://schmerz.example",
		zone:         berlin,
		staffToken:   "the-shared-secret",
		adminGroup:   "schmerz-admins",
		sealer:       testSealer(t, "secret"),
		development:  true,
		developmentIdentity: staffauth.Identity{
			Subject: "sub-dominique", Name: "Dominique", Groups: groups,
		},
	}

	mux := http.NewServeMux()
	if err := c.registerRoutes(mux); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}
	return web.SecurityHeaders()(c.requireSignIn(mux))
}

func get(t *testing.T, handler http.Handler, path string) (*http.Response, string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	raw, _ := io.ReadAll(recorder.Result().Body)
	return recorder.Result(), string(raw)
}

func post(t *testing.T, handler http.Handler, path string, form url.Values) (*http.Response, string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	raw, _ := io.ReadAll(recorder.Result().Body)
	return recorder.Result(), string(raw)
}

// TestEveryConsolePageRendersAndEscapesWhatEditorsTyped walks the console.
//
// A template that refers to a field that does not exist compiles and fails
// when the page is asked for, so every page is asked for — and with every
// text field hostile, a page that printed one unescaped would show it.
func TestEveryConsolePageRendersAndEscapesWhatEditorsTyped(t *testing.T) {
	backend, _ := fakeBackend(t, false)
	console := served(t, backend.URL)

	for _, tc := range []struct {
		path string
		// plain marks a page with nothing an editor typed on it.
		plain bool
	}{
		{path: "/"},
		{path: "/collectives/new", plain: true},
		{path: "/collectives/c1"},
		{path: "/collectives/c1?notice=saved"},
		{path: "/collectives/c1/members/m1"},
		{path: "/collectives/c1/topics"},
		{path: "/collectives/c1/topics/new"},
		{path: "/topics/t1"},
		{path: "/topics/t1/updates/new"},
		{path: "/updates/u1"},
		{path: "/collectives/c1/actions"},
		{path: "/collectives/c1/actions/new"},
		{path: "/collectives/c1/actions/new?topic=t1"},
		{path: "/actions/a1"},
		{path: "/audit"},
		{path: "/audit?page=2"},
		{path: "/settings/theme", plain: true},
		{path: "/auth/signed-out", plain: true},
		{path: "/auth/signed-out?failed=1", plain: true},
		{path: "/?lang=en"},
		{path: "/topics/t1?lang=en"},
		{path: "/actions/a1?lang=en"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			response, rendered := get(t, console, tc.path)
			if response.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200\n%s", response.StatusCode, head(rendered))
			}
			if !strings.Contains(rendered, "</html>") {
				t.Fatalf("the page stopped half way, which is a template failing at render")
			}

			if !tc.plain {
				for _, live := range []string{
					"<script>alert('x')</script>", "<img src=x onerror=alert(1)>", `" onmouseover="alert(2)`,
				} {
					if strings.Contains(rendered, live) {
						t.Errorf("%q was rendered verbatim — it would run as an editor", live)
					}
				}
				if !strings.Contains(rendered, marker) {
					t.Error("the text was dropped rather than escaped")
				}
			}
		})
	}
}

func head(s string) string {
	if len(s) > 500 {
		return s[:500]
	}
	return s
}

// TestANoticeIsAKeyNeverText: `notice` arrives in a link anybody can write,
// and a sentence from a query string shown as the console's own confirmation
// is a way to make it say something it did not.
func TestANoticeIsAKeyNeverText(t *testing.T) {
	backend, _ := fakeBackend(t, false)
	console := served(t, backend.URL)

	_, rendered := get(t, console, "/collectives/c1?notice=saved")
	if !strings.Contains(rendered, "Gespeichert.") {
		t.Error("a known notice was not shown")
	}

	_, rendered = get(t, console, "/collectives/c1?notice="+url.QueryEscape("Ihr Konto wurde gesperrt. Bitte hier anmelden"))
	if strings.Contains(rendered, "gesperrt") {
		t.Error("text from the query string was shown as the console's own notice")
	}
}

// TestAFormIsSentAsWhatTheEditorMeant: the conversions between a form and the
// backend's fields are where meaning is lost — a time read in the wrong zone,
// an amount read as text.
func TestAFormIsSentAsWhatTheEditorMeant(t *testing.T) {
	backend, log := fakeBackend(t, false)
	console := served(t, backend.URL)

	response, _ := post(t, console, "/collectives/c1/topics", url.Values{
		"title": {"100 Millionen weniger"}, "kind": {"cut"}, "level": {"municipal"},
		"amount": {"100.000.000 €"}, "status": {"published"},
		"latitude": {"51.227700"}, "longitude": {"6.773500"}, "zoom": {"11"},
		"place": {"Düsseldorf"},
	})
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("creating a topic = %d, want a redirect", response.StatusCode)
	}
	if got := response.Header.Get("Location"); got != "/topics/t1?notice=created" {
		t.Errorf("went to %q", got)
	}

	sent, ok := log.last(http.MethodPost, "/v1/staff/collectives/c1/topics")
	if !ok {
		t.Fatal("nothing was sent to the backend")
	}
	if sent.Body["amount"] != float64(100_000_000) {
		t.Errorf("amount = %v, want the number the editor wrote", sent.Body["amount"])
	}
	if sent.Body["latitude"] != 51.2277 || sent.Body["zoom"] != float64(11) || sent.Body["place"] != "Düsseldorf" {
		t.Errorf("place = %v / %v / %v", sent.Body["latitude"], sent.Body["zoom"], sent.Body["place"])
	}
	// And it was sent as the editor, with the console's secret.
	if sent.Identity.Subject != "sub-dominique" || sent.Identity.Name != "Dominique" {
		t.Errorf("the write was made as %+v, want the signed-in editor", sent.Identity)
	}

	// An action at 14:00, typed in October: 14:00 in Berlin, which is 12:00
	// UTC. Read in the server's own zone — UTC in a container — it would be
	// announced two hours late.
	response, _ = post(t, console, "/collectives/c1/actions", url.Values{
		"title": {"Demo vor dem Rathaus"}, "kind": {"demonstration"},
		"starts_at": {"2026-10-03T14:00"}, "ends_at": {""}, "status": {"published"},
		"topic_id": {"t1"},
	})
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("creating an action = %d, want a redirect", response.StatusCode)
	}
	sent, _ = log.last(http.MethodPost, "/v1/staff/collectives/c1/actions")
	if sent.Body["starts_at"] != "2026-10-03T14:00:00+02:00" {
		t.Errorf("starts_at = %v, want the time in the installation's zone", sent.Body["starts_at"])
	}
	if _, present := sent.Body["ends_at"]; present {
		t.Errorf("ends_at = %v, want it left out when no end was given", sent.Body["ends_at"])
	}
	if sent.Body["topic_id"] != "t1" {
		t.Errorf("topic_id = %v", sent.Body["topic_id"])
	}
}

// TestARefusedFormKeepsWhatWasTyped, with the backend's own sentence above
// it. An editor who loses three paragraphs to a validation error does not
// write them again.
func TestARefusedFormKeepsWhatWasTyped(t *testing.T) {
	backend, _ := fakeBackend(t, true)
	console := served(t, backend.URL)

	response, rendered := post(t, console, "/topics/t1", url.Values{
		"title": {"Ein Titel, den ich gerade getippt habe"}, "kind": {"reform"}, "level": {"federal"},
		"body":   {"Drei Absätze, die nicht verloren gehen dürfen."},
		"amount": {"2500000"}, "latitude": {"52.52"}, "longitude": {"13.405"}, "zoom": {"12"},
	})
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", response.StatusCode)
	}
	for _, want := range []string{
		"Ein Titel, den ich gerade getippt habe",
		"Drei Absätze, die nicht verloren gehen dürfen.",
		"the title is too long", // the backend's own reason
		`value="2500000"`,       // the amount as typed
		`value="52.52"`,         // and where the pin was put
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the refused form does not show %q", want)
		}
	}
	// The dropdowns are back on what was chosen, not on what is stored.
	if !strings.Contains(rendered, `<option value="reform" selected>`) {
		t.Error("the kind went back to the stored one")
	}

	// A figure that is not a number never reaches the backend at all, and
	// says so in the editor's language.
	response, rendered = post(t, console, "/topics/t1", url.Values{
		"title": {"Titel"}, "kind": {"cut"}, "level": {"municipal"}, "amount": {"100 Mio"},
	})
	if response.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(rendered, "Der Betrag ist keine Zahl") {
		t.Errorf("an unreadable amount: status %d", response.StatusCode)
	}
	if !strings.Contains(rendered, `value="100 Mio"`) {
		t.Error("the amount as typed was not put back in the field")
	}
}

// TestDeletingACollectiveNeedsItsAddressTyped: everything it ever published
// goes with it, and a button alone is one stray click from that.
func TestDeletingACollectiveNeedsItsAddressTyped(t *testing.T) {
	backend, log := fakeBackend(t, false)
	console := served(t, backend.URL)

	response, rendered := post(t, console, "/collectives/c1/delete", url.Values{"confirm": {"nearly"}})
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want the deletion refused", response.StatusCode)
	}
	if !strings.Contains(rendered, "Es wurde nichts gelöscht") {
		t.Error("the page does not say nothing was deleted")
	}
	if _, sent := log.last(http.MethodDelete, "/v1/staff/collectives/c1"); sent {
		t.Fatal("the collective was deleted without its address being typed")
	}

	response, _ = post(t, console, "/collectives/c1/delete", url.Values{"confirm": {"buendnis"}})
	if response.StatusCode != http.StatusSeeOther {
		t.Errorf("status = %d, want a redirect after deleting", response.StatusCode)
	}
	if _, sent := log.last(http.MethodDelete, "/v1/staff/collectives/c1"); !sent {
		t.Error("the confirmed deletion was not sent")
	}
}

// TestWhatAnEditorCannotUseIsNotDrawn — and the backend, not this, is what
// refuses.
func TestWhatAnEditorCannotUseIsNotDrawn(t *testing.T) {
	backend, _ := fakeBackend(t, false)

	_, asAdmin := get(t, served(t, backend.URL), "/collectives/c1")
	_, asEditor := get(t, served(t, backend.URL, "duesseldorf"), "/collectives/c1")

	if !strings.Contains(asAdmin, `href="/settings/theme"`) {
		t.Error("an administrator is not offered the theme page")
	}
	if strings.Contains(asEditor, `href="/settings/theme"`) {
		t.Error("an editor is offered the theme page")
	}
	if strings.Contains(asEditor, `/collectives/c1/delete`) {
		t.Error("an editor is offered the deletion of a collective")
	}
	// The address and the group are shown to an editor — they should be able
	// to see which group they are here because of — and are not editable.
	if !strings.Contains(asEditor, `id="slug" name="slug"`) || !strings.Contains(asEditor, "readonly") {
		t.Error("an editor is not shown the address and the group as read-only")
	}

	response, _ := get(t, served(t, backend.URL, "duesseldorf"), "/collectives/new")
	if response.StatusCode != http.StatusForbidden {
		t.Errorf("an editor opening the new-collective form = %d, want 403", response.StatusCode)
	}
}
