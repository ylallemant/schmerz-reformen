package console

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"slices"
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
	Query    url.Values
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
		Website: "https://example.org/", Contact: hostile, Status: "published",
		AdminGroup: "schmerz-collective-buendnis-admins", AuthorGroup: hostile,
		Place: hostile, Latitude: 51.2277, Longitude: 6.7735, LogoID: "logo-1",
		Members: []apiclient.Member{{ID: "m1", OrganisationID: "o1", Name: hostile, Kind: "union",
			Place: hostile, Position: 1}},
	}
	values := apiclient.OrganisationValues{
		Name: hostile, Kind: "union", Website: "https://example.org/", ParentID: "o2",
		ParentName: hostile, LogoID: "logo-2", Place: hostile, Latitude: 51.2277, Longitude: 6.7735,
	}
	organisation := apiclient.Organisation{
		ID: "o1", Slug: "verdi", OrganisationValues: values,
		AdminGroup: "schmerz-organisation-verdi-admins", MemberGroup: hostile,
		Collectives: []apiclient.CollectiveRef{{ID: "c1", Name: hostile, Slug: "buendnis"}},
	}
	parent := apiclient.Organisation{ID: "o2", OrganisationValues: apiclient.OrganisationValues{
		Name: hostile, Kind: "union", Place: hostile,
	}}
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

	people := apiclient.EntryPeople{
		Admins: []apiclient.User{{PK: 7, Username: hostile, Name: hostile}},
		Others: []apiclient.User{{PK: 8, Username: "kai", Name: hostile}},
	}
	users := []apiclient.User{{PK: 7, Username: hostile, Name: hostile}, {PK: 9, Username: "sam", Name: hostile}}

	// What the backend decides per editor: the site's administrators, and
	// whoever is in an entry's administrators' group, run it.
	administers := func(r *http.Request, group string) bool {
		identity, _ := staffauth.Decode(r.Header.Get(staffauth.IdentityHeader))
		return slices.Contains(identity.Groups, "schmerz-admins") || slices.Contains(identity.Groups, group)
	}
	collectiveFor := func(r *http.Request) apiclient.Collective {
		mine := collective
		mine.Administers = administers(r, collective.AdminGroup)
		mine.Authors = true
		return mine
	}
	organisationFor := func(r *http.Request, o apiclient.Organisation) apiclient.Organisation {
		o.Administers = administers(r, o.AdminGroup)
		return o
	}

	answer := func(w http.ResponseWriter, body any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(body) //nolint:errcheck
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/staff/me", func(w http.ResponseWriter, r *http.Request) {
		answer(w, apiclient.StaffProfile{Subject: "development", Admin: administers(r, ""), Allowed: true,
			Collectives:   []apiclient.Collective{collectiveFor(r)},
			Organisations: []apiclient.Organisation{organisationFor(r, organisation)}})
	})
	mux.HandleFunc("GET /v1/staff/collectives/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "c1" {
			http.Error(w, `{"detail":"no such collective"}`, http.StatusNotFound)
			return
		}
		answer(w, collectiveFor(r))
	})
	mux.HandleFunc("GET /v1/staff/collectives/{id}/people", func(w http.ResponseWriter, r *http.Request) {
		mine := people
		mine.MayGrantAdmins = administers(r, "")
		answer(w, mine)
	})
	mux.HandleFunc("GET /v1/staff/organisations/{id}/people", func(w http.ResponseWriter, _ *http.Request) {
		answer(w, people)
	})
	mux.HandleFunc("GET /v1/staff/users", func(w http.ResponseWriter, _ *http.Request) {
		answer(w, map[string]any{"users": users})
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
	mux.HandleFunc("GET /v1/staff/organisations", func(w http.ResponseWriter, r *http.Request) {
		answer(w, map[string]any{"organisations": []apiclient.Organisation{
			organisationFor(r, organisation), organisationFor(r, parent)}, "total": 2})
	})
	mux.HandleFunc("GET /v1/staff/organisations/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "o1" {
			http.Error(w, `{"detail":"no such organisation"}`, http.StatusNotFound)
			return
		}
		answer(w, organisationFor(r, organisation))
	})
	mux.HandleFunc("GET /v1/staff/people", func(w http.ResponseWriter, _ *http.Request) {
		answer(w, apiclient.People{
			AdminGroup: "schmerz-admins", UsersGroup: "schmerz-users", RecoveryReady: false,
			People: []apiclient.Person{
				{PK: 7, Username: hostile, Name: hostile, Admin: true, Self: true, Roles: []apiclient.RoleRef{
					{Kind: "collective", ID: "c1", Name: hostile, Role: "admins"},
					{Kind: "collective", ID: "c1", Name: hostile, Role: "authors"},
					{Kind: "organisation", ID: "o1", Name: hostile, Role: "admins"},
					{Kind: "organisation", ID: "o1", Name: hostile, Role: "members"},
				}},
				{PK: 8, Username: "kai", Name: hostile},
			},
		})
	})
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
	mux.HandleFunc("POST /v1/staff/people", func(w http.ResponseWriter, r *http.Request) {
		// Somebody the directory has already is adopted, with no link.
		var sent struct {
			Username string `json:"username"`
		}
		json.NewDecoder(r.Body).Decode(&sent) //nolint:errcheck
		if sent.Username == "existing" && !refuse {
			answer(w, apiclient.WayIn{Username: "existing", Adopted: true})
			return
		}
		write(apiclient.WayIn{Username: "kai", Link: "https://auth.example/if/flow/recovery/?token=once"})(w, r)
	})
	mux.HandleFunc("POST /v1/staff/people/{pk}/link", write(apiclient.WayIn{Username: "kai", Link: "https://auth.example/if/flow/recovery/?token=again"}))
	mux.HandleFunc("PUT /v1/staff/people/{pk}", write(map[string]bool{"done": true}))
	mux.HandleFunc("DELETE /v1/staff/people/{pk}", write(map[string]bool{"done": true}))
	mux.HandleFunc("POST /v1/staff/organisations", write(organisation))
	mux.HandleFunc("PUT /v1/staff/organisations/{id}", write(organisation))
	mux.HandleFunc("DELETE /v1/staff/organisations/{id}", write(map[string]bool{"done": true}))
	for _, kind := range []string{"collectives", "organisations"} {
		mux.HandleFunc("PUT /v1/staff/"+kind+"/{id}/people/{role}/{pk}", write(map[string]bool{"done": true}))
		mux.HandleFunc("DELETE /v1/staff/"+kind+"/{id}/people/{role}/{pk}", write(map[string]bool{"done": true}))
	}
	mux.HandleFunc("DELETE /v1/staff/topics/{id}", write(map[string]bool{"done": true}))
	mux.HandleFunc("DELETE /v1/staff/collectives/{id}", write(map[string]bool{"done": true}))

	guarded := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, err := staffauth.Decode(r.Header.Get(staffauth.IdentityHeader))
		token := r.Header.Get(staffauth.TokenHeader)
		if err != nil || token != "the-shared-secret" {
			http.Error(w, `{"detail":"this route belongs to the console"}`, http.StatusUnauthorized)
			return
		}

		entry := received{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Identity: identity, Token: token}
		if r.Method != http.MethodGet {
			raw, _ := io.ReadAll(r.Body)
			json.Unmarshal(raw, &entry.Body) //nolint:errcheck // an empty body is simply nil
			// Put back, for a handler that answers by what was sent.
			r.Body = io.NopCloser(bytes.NewReader(raw))
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
	c := testConsole(t, backendURL, groups...)
	mux := http.NewServeMux()
	if err := c.registerRoutes(mux); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}
	return web.SecurityHeaders()(c.requireSignIn(mux))
}

// testConsole builds the console, signed in as the development identity.
func testConsole(t *testing.T, backendURL string, groups ...string) *console {
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
	return c
}

func get(t *testing.T, handler http.Handler, path string) (*http.Response, string) {
	t.Helper()
	path, language := inLanguage(path)
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if language != nil {
		request.AddCookie(language)
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	raw, _ := io.ReadAll(recorder.Result().Body)
	return recorder.Result(), string(raw)
}

// inLanguage reads a test path written as "/calendar#en": the part after the
// hash is the language the reader chose, carried as the browser carries it —
// in the language cookie, never in the address. The path itself is what is
// requested.
func inLanguage(path string) (string, *http.Cookie) {
	path, lang, found := strings.Cut(path, "#")
	if !found {
		return path, nil
	}
	return path, &http.Cookie{Name: web.LanguageCookie, Value: lang}
}

func post(t *testing.T, handler http.Handler, path string, form url.Values) (*http.Response, string) {
	t.Helper()
	path, language := inLanguage(path)
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if language != nil {
		request.AddCookie(language)
	}

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
		{path: "/organisations"},
		{path: "/organisations/new"},
		{path: "/organisations/o1"},
		{path: "/organisations/o1?notice=saved#de"},
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
		{path: "/settings/people"},
		{path: "/settings/people#de"},
		{path: "/auth/signed-out", plain: true},
		{path: "/auth/signed-out?failed=1", plain: true},
		{path: "/#en"},
		{path: "/topics/t1#en"},
		{path: "/actions/a1#en"},
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

	_, rendered := get(t, console, "/collectives/c1?notice=saved#de")
	if !strings.Contains(rendered, "Gespeichert.") {
		t.Error("a known notice was not shown")
	}

	_, rendered = get(t, console, "/collectives/c1?notice="+url.QueryEscape("Ihr Konto wurde gesperrt. Bitte hier anmelden"))
	// Looked for in the notice itself: the address is on the page elsewhere,
	// escaped, as the language switcher's way back.
	if strings.Contains(rendered, `class="form-notice"`) {
		t.Error("an unknown notice drew a notice at all")
	}
	if strings.Contains(rendered, ">Ihr Konto wurde gesperrt") {
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
	response, rendered = post(t, console, "/topics/t1#de", url.Values{
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

	response, rendered := post(t, console, "/collectives/c1/delete#de", url.Values{"confirm": {"nearly"}})
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
	_, asCollectiveAdmin := get(t, served(t, backend.URL, "schmerz-collective-buendnis-admins"), "/collectives/c1")
	_, asAuthor := get(t, served(t, backend.URL, "schmerz-collective-buendnis-authors"), "/collectives/c1")

	if !strings.Contains(asAdmin, `href="/settings/theme"`) {
		t.Error("an administrator is not offered the theme page")
	}
	if strings.Contains(asCollectiveAdmin, `href="/settings/theme"`) {
		t.Error("a collective's administrator is offered the theme page")
	}
	if strings.Contains(asCollectiveAdmin, `/collectives/c1/delete`) {
		t.Error("a collective's administrator is offered its deletion")
	}

	// A collective's administrator edits it and chooses its authors, and
	// not its administrators: those are the site's to choose.
	if strings.Contains(asCollectiveAdmin, " disabled>") {
		t.Error("the collective's own administrator is shown a locked form")
	}
	if !strings.Contains(asCollectiveAdmin, `id="give-author"`) {
		t.Error("a collective's administrator is not offered to choose an author")
	}
	if strings.Contains(asCollectiveAdmin, `id="give-admin"`) {
		t.Error("a collective's administrator is offered to choose another")
	}
	if !strings.Contains(asAdmin, `id="give-admin"`) {
		t.Error("the site's administrator is not offered to choose a collective's")
	}
	// The address and the groups are shown — an editor should see which
	// group they are here because of — and are not editable.
	if !strings.Contains(asCollectiveAdmin, `id="slug" name="slug"`) || !strings.Contains(asCollectiveAdmin, "readonly") ||
		!strings.Contains(asCollectiveAdmin, "schmerz-collective-buendnis-admins") {
		t.Error("the address and the groups are not shown read-only")
	}

	// An author writes topics, updates and actions, and reads the profile.
	if !strings.Contains(asAuthor, " disabled>") {
		t.Error("an author is shown the collective's profile as editable")
	}
	for _, offered := range []string{`/collectives/c1/logo`, `id="give-author"`, `action="/collectives/c1/members"`} {
		if strings.Contains(asAuthor, offered) {
			t.Errorf("an author is offered %s", offered)
		}
	}

	response, _ := get(t, served(t, backend.URL, "schmerz-collective-buendnis-admins"), "/collectives/new")
	if response.StatusCode != http.StatusForbidden {
		t.Errorf("an editor opening the new-collective form = %d, want 403", response.StatusCode)
	}
	response, _ = get(t, served(t, backend.URL, "schmerz-organisation-verdi-admins"), "/organisations/new")
	if response.StatusCode != http.StatusForbidden {
		t.Errorf("an organisation's administrator opening the new-organisation form = %d, want 403", response.StatusCode)
	}
}

// TestAnOrganisationIsRunByItsAdministrators: they edit it and choose its
// people; anybody else reads it; only the site's administrators create and
// delete one.
func TestAnOrganisationIsRunByItsAdministrators(t *testing.T) {
	backend, log := fakeBackend(t, false)

	_, asOwn := get(t, served(t, backend.URL, "schmerz-organisation-verdi-admins"), "/organisations/o1")
	_, asOther := get(t, served(t, backend.URL, "schmerz-collective-buendnis-authors"), "/organisations/o1")
	_, asAdmin := get(t, served(t, backend.URL), "/organisations/o1")

	if !strings.Contains(asOwn, `action="/organisations/o1"`) || !strings.Contains(asOwn, `id="give-member"`) ||
		!strings.Contains(asOwn, `id="give-admin"`) {
		t.Error("an organisation's administrator is not offered its form and its people")
	}
	if strings.Contains(asOwn, "/organisations/o1/delete") {
		t.Error("an organisation's administrator is offered its deletion")
	}
	for _, offered := range []string{`action="/organisations/o1"`, "/organisations/o1/logo", "/organisations/o1/people"} {
		if strings.Contains(asOther, offered) {
			t.Errorf("somebody who does not run the organisation is offered %s", offered)
		}
	}
	if !strings.Contains(asAdmin, "/organisations/o1/delete") {
		t.Error("the site's administrator is not offered the deletion")
	}

	console := served(t, backend.URL)
	response, _ := post(t, console, "/organisations", url.Values{
		"name": {"ver.di Düsseldorf"}, "kind": {"union"}, "website": {"https://example.org"},
		"parent_id": {" o2 "}, "latitude": {"51.2277"}, "longitude": {"6.7735"}, "zoom": {"17"},
		"place": {"Karlstraße 123"},
	})
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/organisations/o1?notice=created" {
		t.Fatalf("creating = %d to %q", response.StatusCode, response.Header.Get("Location"))
	}
	sent, _ := log.last(http.MethodPost, "/v1/staff/organisations")
	if sent.Body["name"] != "ver.di Düsseldorf" || sent.Body["parent_id"] != "o2" ||
		sent.Body["zoom"] != float64(17) || sent.Body["place"] != "Karlstraße 123" {
		t.Errorf("sent %v", sent.Body)
	}

	// Giving a role from the picker sends the key; taking one from a row
	// sends the username along, for the backend's log.
	response, _ = post(t, console, "/organisations/o1/people", url.Values{
		"role": {"members"}, "pk": {"9"}, "action": {"give"},
	})
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/organisations/o1?notice=saved#people" {
		t.Fatalf("giving = %d to %q", response.StatusCode, response.Header.Get("Location"))
	}
	if _, ok := log.last(http.MethodPut, "/v1/staff/organisations/o1/people/members/9"); !ok {
		t.Error("the role was not given")
	}
	post(t, console, "/collectives/c1/people", url.Values{
		"role": {"authors"}, "pk": {"8"}, "username": {"kai"}, "action": {"take"},
	})
	if sent, ok := log.last(http.MethodDelete, "/v1/staff/collectives/c1/people/authors/8"); !ok || sent.Query.Get("username") != "kai" {
		t.Errorf("the role was not taken, or not by name: %v", sent.Query)
	}

	response, _ = post(t, console, "/collectives/c1/members", url.Values{"organisation_id": {"o2"}})
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("adding a member = %d", response.StatusCode)
	}
	sent, _ = log.last(http.MethodPost, "/v1/staff/collectives/c1/members")
	if sent.Body["organisation_id"] != "o2" {
		t.Errorf("member sent as %v, want the organisation chosen", sent.Body)
	}
}

// TestTheMemberPickerOffersOnlyWhatIsNotListedYet: an organisation already in
// the collective is not offered again, and an organisation is never offered
// as its own parent.
func TestTheMemberPickerOffersOnlyWhatIsNotListedYet(t *testing.T) {
	backend, _ := fakeBackend(t, false)
	console := served(t, backend.URL)

	_, rendered := get(t, console, "/collectives/c1")
	if strings.Contains(rendered, `<option value="o1"`) {
		t.Error("the organisation already in the collective is offered again")
	}
	if !strings.Contains(rendered, `<option value="o2"`) {
		t.Error("an organisation not yet in the collective is not offered")
	}

	_, rendered = get(t, console, "/organisations/o1")
	if strings.Contains(rendered, `<option value="o1"`) {
		t.Error("an organisation is offered as its own parent")
	}
	if !strings.Contains(rendered, `<option value="o2" data-search=`) || !strings.Contains(rendered, `selected>`) {
		t.Error("the current parent is not offered, or not chosen")
	}
}

// TestAPageOutlivesItsDirectory: the people of a collective or an
// organisation are in the identity provider, the rest of its page in the
// database. Before the setup wizard has run, or while Authentik is down, the
// page is still drawn, and says why its people are not.
func TestAPageOutlivesItsDirectory(t *testing.T) {
	backend, _ := fakeBackend(t, false)
	target, _ := url.Parse(backend.URL)
	proxy := httputil.NewSingleHostReverseProxy(target)
	away := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/people") || r.URL.Path == "/v1/staff/users" {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"detail":"no identity provider is configured: run the console's setup wizard first"}`)) //nolint:errcheck
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(away.Close)
	console := served(t, away.URL)

	for _, path := range []string{"/collectives/c1", "/organisations/o1", "/settings/people"} {
		response, rendered := get(t, console, path)
		if response.StatusCode != http.StatusOK {
			t.Errorf("%s with the directory away = %d, want the page", path, response.StatusCode)
		}
		if !strings.Contains(rendered, "no identity provider is configured") {
			t.Errorf("%s does not say why its people are missing", path)
		}
	}
}
