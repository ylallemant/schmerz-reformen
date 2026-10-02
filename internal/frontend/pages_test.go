package frontend

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ylallemant/schmerz-reformen/internal/apiclient"
	"github.com/ylallemant/schmerz-reformen/internal/web"
)

// hostile is what every text field of the fake backend's answers carries.
//
// The pages show what editors typed, and one compromised editor account is
// all it takes for that to be this. Each fragment is a different way markup
// gets live: an element, a handler in an attribute, and an attempt to break
// out of the attribute it was put in.
const hostile = `<script>alert('x')</script><img src=x onerror=alert(1)>" onmouseover="alert(2)`

// marker is plain text inside the hostile string that must survive as text:
// without it a template that simply dropped the field would pass.
const marker = "onmouseover"

// when is a fixed moment, in the summer so the offset is not the winter one.
var when = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

// fakeBackend answers the public API with content that is hostile wherever a
// person could have typed it.
//
// It stands in for the backend rather than using the real one because this
// service must not link it, and because what is under test here is the
// rendering: every page, drawn from data that attacks it.
func fakeBackend(t *testing.T) *httptest.Server {
	t.Helper()

	collectiveRef := &apiclient.CollectiveRef{ID: "c1", Name: hostile, Slug: "buendnis"}
	topicRef := &apiclient.TopicRef{ID: "t1", Title: hostile, Kind: "cut", Place: hostile}

	collective := apiclient.Collective{
		ID: "c1", Name: hostile, Slug: "buendnis", Summary: hostile, Description: hostile,
		Website: "https://example.org/", Contact: hostile, Status: "published",
		Place: hostile, Latitude: 51.2277, Longitude: 6.7735, Followers: 3,
		Members: []apiclient.Member{
			{ID: "m1", Name: hostile, Kind: "union", Website: "https://example.org/"},
			{ID: "m2", Name: hostile, Kind: "party"},
		},
	}
	topic := apiclient.Topic{
		ID: "t1", CollectiveID: "c1", Collective: collectiveRef,
		Kind: "cut", Level: "municipal", Title: hostile, Summary: hostile, Body: hostile,
		Amount: 100_000_000, SourceURL: "https://example.org/vorlage",
		Place: hostile, Latitude: 51.2277, Longitude: 6.7735,
		Status: "published", PublishedAt: &when, UpdatedAt: when, Followers: 12,
	}
	update := apiclient.Update{
		ID: "u1", TopicID: "t1", Topic: topicRef, CollectiveID: "c1", Collective: collectiveRef,
		Title: hostile, Body: hostile, Excerpt: hostile, Truncated: true,
		SourceURL: "https://example.org/bericht", Status: "published",
		PublishedAt: &when, UpdatedAt: when,
	}
	ends := when.Add(2 * time.Hour)
	action := apiclient.Action{
		ID: "a1", CollectiveID: "c1", Collective: collectiveRef, TopicID: "t1", Topic: topicRef,
		Kind: "demonstration", Title: hostile, Description: hostile,
		// Far in the future, so the page offers "I'm coming" whenever this
		// test runs.
		StartsAt: time.Now().Add(240 * time.Hour), EndsAt: &ends,
		Place: hostile, Latitude: 51.2259, Longitude: 6.7724,
		ExternalURL: "https://example.org/demo", Status: "published",
		PublishedAt: &when, UpdatedAt: when, Participants: 41,
	}
	cancelled := action
	cancelled.ID, cancelled.Cancelled = "a2", true

	answer := func(w http.ResponseWriter, body any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(body) //nolint:errcheck
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/map", func(w http.ResponseWriter, _ *http.Request) {
		answer(w, apiclient.MapView{
			Topics: []apiclient.Topic{topic}, Actions: []apiclient.Action{action, cancelled},
			Collectives: []apiclient.Collective{collective},
			Totals:      apiclient.MapTotals{Topics: 412, Actions: 37, Collectives: 58, Amount: 2_500_000_000},
		})
	})
	mux.HandleFunc("GET /v1/collectives", func(w http.ResponseWriter, _ *http.Request) {
		answer(w, map[string]any{"collectives": []apiclient.Collective{collective}, "total": 1})
	})
	mux.HandleFunc("GET /v1/collectives/{slug}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("slug") != "buendnis" {
			http.Error(w, `{"detail":"no such collective"}`, http.StatusNotFound)
			return
		}
		answer(w, collective)
	})
	mux.HandleFunc("GET /v1/topics", func(w http.ResponseWriter, _ *http.Request) {
		answer(w, map[string]any{"topics": []apiclient.Topic{topic}, "total": 1})
	})
	mux.HandleFunc("GET /v1/topics/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "t1" {
			http.Error(w, `{"detail":"no such topic"}`, http.StatusNotFound)
			return
		}
		answer(w, topic)
	})
	mux.HandleFunc("GET /v1/updates", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("following") != "" && r.Header.Get("Authorization") == "" {
			http.Error(w, `{"detail":"sign in first"}`, http.StatusUnauthorized)
			return
		}
		answer(w, map[string]any{"updates": []apiclient.Update{update}, "total": 45})
	})
	mux.HandleFunc("GET /v1/updates/{id}", func(w http.ResponseWriter, _ *http.Request) { answer(w, update) })
	mux.HandleFunc("GET /v1/actions", func(w http.ResponseWriter, _ *http.Request) {
		answer(w, map[string]any{"actions": []apiclient.Action{action, cancelled}, "total": 2})
	})
	mux.HandleFunc("GET /v1/actions/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "a2" {
			answer(w, cancelled)
			return
		}
		answer(w, action)
	})
	mux.HandleFunc("GET /v1/accounts/me/following", func(w http.ResponseWriter, _ *http.Request) {
		answer(w, apiclient.Following{
			Collectives: []apiclient.Collective{collective}, Topics: []apiclient.Topic{topic},
		})
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// served builds the whole site on a router of its own, in front of a backend.
func served(t *testing.T, backendURL string) http.Handler {
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

	s := &site{
		siteURL:      "https://schmerz.example",
		siteName:     "schMERZ-Reformen",
		assetVersion: "test",
		renderer:     renderer,
		localization: localization,
		backend:      backend,
		zone:         berlin,
		overlay:      web.NewThemeOverlay(backend),
		assets:       web.NewThemeAssetHandler(backend),
		files:        web.NewThemeFileHandler(backend),
		proxies:      web.DefaultTrustedProxies(),
	}

	mux := http.NewServeMux()
	if err := s.registerRoutes(mux); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}
	// Behind the same policy middleware the service uses, so each page is
	// rendered with a nonce as it would be.
	return web.SecurityHeaders()(mux)
}

// fetch requests one page, optionally as somebody carrying a session.
func fetch(t *testing.T, handler http.Handler, method, path string, signedIn bool, form string) *http.Response {
	t.Helper()

	var body io.Reader
	if form != "" {
		body = strings.NewReader(form)
	}
	request := httptest.NewRequest(method, path, body)
	if form != "" {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if signedIn {
		request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "a-session-token"})
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder.Result()
}

func bodyOf(t *testing.T, response *http.Response) string {
	t.Helper()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(raw)
}

// assertNeutralised checks the two halves of the guarantee: what was typed is
// shown, and none of it can run.
func assertNeutralised(t *testing.T, where, rendered string) {
	t.Helper()

	// Nothing that was typed appears as it was typed. This is the whole
	// property: markup in, text out.
	for _, live := range []string{
		"<script>alert('x')</script>",
		"<img src=x onerror=alert(1)>",
		`" onmouseover="alert(2)`,
	} {
		if strings.Contains(rendered, live) {
			t.Errorf("%s: %q was rendered verbatim — it would run in a reader's browser", where, live)
		}
	}
	// And the words are still there: escaping, not swallowing.
	if !strings.Contains(rendered, marker) {
		t.Errorf("%s: the text was dropped rather than escaped", where)
	}
}

// TestEveryPageRendersAndEscapesWhatEditorsTyped walks the whole public site.
//
// It is two tests in one, and both are the kind only rendering can do. A
// template that refers to a field that does not exist compiles perfectly and
// fails when the page is asked for — so every page is asked for. And every
// text an editor could have typed is hostile, so a page that printed one
// unescaped would show it here.
func TestEveryPageRendersAndEscapesWhatEditorsTyped(t *testing.T) {
	site := served(t, fakeBackend(t).URL)

	for _, tc := range []struct {
		path     string
		signedIn bool
		// plain marks a page with no editor-written text on it, where there
		// is nothing to escape and only the rendering is checked.
		plain bool
	}{
		{path: "/"},
		{path: "/topics"},
		{path: "/topics?kind=cut&level=municipal"},
		{path: "/topics/t1"},
		{path: "/topics/t1", signedIn: true},
		{path: "/feed"},
		{path: "/feed?page=2"},
		{path: "/feed?following=1", signedIn: true},
		{path: "/updates/u1"},
		{path: "/calendar"},
		{path: "/calendar?mine=1", signedIn: true},
		{path: "/actions/a1"},
		{path: "/actions/a1", signedIn: true},
		{path: "/actions/a2"},
		{path: "/collectives"},
		{path: "/collectives/buendnis"},
		{path: "/collectives/buendnis", signedIn: true},
		{path: "/account/following", signedIn: true},
		{path: "/about", plain: true},
		{path: "/account/signin", plain: true},
		{path: "/account/signin?recover=1&next=/topics/t1", plain: true},
		// The same pages in the other language: a catalogue with a broken
		// template expression in it fails at render, not at load.
		{path: "/?lang=en"},
		{path: "/calendar?lang=en"},
		{path: "/topics/t1?lang=en"},
		{path: "/about?lang=en", plain: true},
	} {
		name := tc.path
		if tc.signedIn {
			name += " (signed in)"
		}
		t.Run(name, func(t *testing.T) {
			response := fetch(t, site, http.MethodGet, tc.path, tc.signedIn, "")
			rendered := bodyOf(t, response)

			if response.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200\n%s", response.StatusCode, firstLines(rendered))
			}
			if !strings.Contains(rendered, "</html>") {
				t.Fatalf("the page stopped half way, which is a template failing at render:\n%s", lastLines(rendered))
			}
			if !tc.plain {
				assertNeutralised(t, tc.path, rendered)
			}
			// A key with no translation renders as the key. The catalogue
			// test catches the ones it can find in the source; this catches
			// the ones assembled at render time from a value.
			for _, family := range []string{"kind.", "level.", "action_kind.", "member_kind.", "status.", "format.", "month.", "weekday"} {
				if leaked := untranslated(rendered, family); leaked != "" {
					t.Errorf("an untranslated key reached the page: %q", leaked)
				}
			}
		})
	}
}

// untranslated finds a catalogue key printed as itself, between tags.
func untranslated(rendered, family string) string {
	index := strings.Index(rendered, ">"+family)
	if index < 0 {
		return ""
	}
	end := strings.IndexAny(rendered[index+1:], "< \n")
	if end < 0 {
		return rendered[index+1:]
	}
	return rendered[index+1 : index+1+end]
}

func firstLines(s string) string {
	if len(s) > 600 {
		return s[:600]
	}
	return s
}

func lastLines(s string) string {
	if len(s) > 600 {
		return s[len(s)-600:]
	}
	return s
}

// TestWhatIsNotThereIsAPageThatSaysSo, rather than a backend error shown to a
// reader: a topic taken back to draft answers exactly like one that never
// existed.
func TestWhatIsNotThereIsAPageThatSaysSo(t *testing.T) {
	site := served(t, fakeBackend(t).URL)

	for _, path := range []string{"/topics/gone", "/collectives/nobody", "/nothing/here"} {
		response := fetch(t, site, http.MethodGet, path, false, "")
		if response.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, response.StatusCode)
		}
	}

	// And the page for it is ours, in the reader's language, with a way back.
	rendered := bodyOf(t, fetch(t, site, http.MethodGet, "/topics/gone", false, ""))
	if !strings.Contains(rendered, "Zur Karte") {
		t.Error("the not-found page offers no way back")
	}
}

// TestAPageForSomebodyNeedsSomebody: "what I follow" asked by nobody is sent
// to sign in with the way back attached — never shown everybody's feed under
// a heading that says it is theirs.
func TestAPageForSomebodyNeedsSomebody(t *testing.T) {
	site := served(t, fakeBackend(t).URL)

	for path, want := range map[string]string{
		"/feed?following=1":  "/account/signin?next=%2Ffeed%3Ffollowing%3D1",
		"/calendar?mine=1":   "/account/signin?next=%2Fcalendar%3Fmine%3D1",
		"/account/following": "/account/signin?next=%2Faccount%2Ffollowing",
		"/account":           "/account/signin?next=%2Faccount",
		"/notifications":     "/account/signin?next=%2Fnotifications",
	} {
		response := fetch(t, site, http.MethodGet, path, false, "")
		if response.StatusCode != http.StatusSeeOther {
			t.Errorf("GET %s = %d, want a redirect to sign in", path, response.StatusCode)
			continue
		}
		if got := response.Header.Get("Location"); got != want {
			t.Errorf("GET %s went to %q, want %q", path, got, want)
		}
	}
}

// TestAButtonPressedByNobodyIsAnInvitation: the follow and attend buttons are
// shown to everybody, and somebody with no account pressing one is sent to
// make one and brought back to the page they were on.
func TestAButtonPressedByNobodyIsAnInvitation(t *testing.T) {
	site := served(t, fakeBackend(t).URL)

	response := fetch(t, site, http.MethodPost, "/follow/topic/t1", false, "next=%2Ftopics%2Ft1")
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want a redirect", response.StatusCode)
	}
	if got := response.Header.Get("Location"); got != "/account/signin?next=%2Ftopics%2Ft1" {
		t.Errorf("went to %q, want sign-in with the way back", got)
	}

	// The way back is held to this site: a form on somebody else's page must
	// not be able to use this one as a redirector.
	response = fetch(t, site, http.MethodPost, "/actions/a1/attend", false, "next=https%3A%2F%2Fevil.example%2F")
	if got := response.Header.Get("Location"); got != "/account/signin?next=%2Factions%2Fa1" {
		t.Errorf("went to %q, want the action's own page as the way back", got)
	}

	// Only the two things that can be followed can be followed.
	response = fetch(t, site, http.MethodPost, "/follow/account/somebody", true, "")
	if response.StatusCode != http.StatusNotFound {
		t.Errorf("following an account = %d, want 404", response.StatusCode)
	}
}

// TestTheFeedIsWellFormedWhateverEditorsTyped, end to end through the handler.
func TestTheFeedIsWellFormedWhateverEditorsTyped(t *testing.T) {
	site := served(t, fakeBackend(t).URL)

	response := fetch(t, site, http.MethodGet, "/feed.xml", false, "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/atom+xml") {
		t.Errorf("content type = %q", got)
	}

	document := bodyOf(t, response)
	// Text, not markup: a feed reader shows what was typed and runs none of
	// it. The angle brackets must arrive as entities.
	if strings.Contains(document, "<script>") {
		t.Error("the feed carries a live script element")
	}
	if !strings.Contains(document, "&lt;script&gt;") {
		t.Error("the title's markup did not arrive as text")
	}
	// Links are absolute: a relative one means nothing in a feed reader.
	if !strings.Contains(document, `href="https://schmerz.example/updates/u1"`) {
		t.Error("the entry does not link to its own page in full")
	}
}

// TestACalendarFileCannotBeRewrittenByATitle, end to end through the handler.
func TestACalendarFileCannotBeRewrittenByATitle(t *testing.T) {
	site := served(t, fakeBackend(t).URL)

	for _, path := range []string{"/calendar.ics", "/collectives/buendnis/calendar.ics", "/actions/a1/calendar.ics"} {
		response := fetch(t, site, http.MethodGet, path, false, "")
		if response.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d", path, response.StatusCode)
			continue
		}
		if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/calendar") {
			t.Errorf("GET %s content type = %q", path, got)
		}
		// The filename is one this code chose. A header built from a title
		// is how a header gets split.
		if disposition := response.Header.Get("Content-Disposition"); strings.ContainsAny(disposition, "<>\r\n") {
			t.Errorf("GET %s has a filename built from content: %q", path, disposition)
		}

		document := strings.ReplaceAll(bodyOf(t, response), "\r\n ", "")
		if !strings.HasPrefix(document, "BEGIN:VCALENDAR\r\n") || !strings.HasSuffix(document, "END:VCALENDAR\r\n") {
			t.Errorf("GET %s is not a calendar", path)
		}
		if !strings.Contains(document, "\r\nURL:https://schmerz.example/actions/a1\r\n") {
			t.Errorf("GET %s does not link the event to its page", path)
		}
	}

	// The whole calendar carries the cancelled action, saying so.
	document := strings.ReplaceAll(bodyOf(t, fetch(t, site, http.MethodGet, "/calendar.ics", false, "")), "\r\n ", "")
	if strings.Count(document, "\r\nBEGIN:VEVENT\r\n") != 2 || !strings.Contains(document, "\r\nSTATUS:CANCELLED\r\n") {
		t.Error("the cancelled action is missing from the calendar, or does not say it is cancelled")
	}
}

// TestTheMapIsHandedWordsNotMarkup: everything a popup shows arrives already
// worded, and as JSON strings the script places with textContent.
func TestTheMapIsHandedWordsNotMarkup(t *testing.T) {
	site := served(t, fakeBackend(t).URL)

	response := fetch(t, site, http.MethodGet, "/api/map?bounds=51.4,51.1,6.95,6.6", false, "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}

	var answer mapAnswer
	if err := json.NewDecoder(response.Body).Decode(&answer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(answer.Items) != 4 {
		t.Fatalf("%d items, want a topic, two actions and a collective", len(answer.Items))
	}

	layers := map[string]mapItem{}
	for _, item := range answer.Items {
		layers[item.Layer] = item
		if !strings.HasPrefix(item.Href, "/") || strings.HasPrefix(item.Href, "//") {
			t.Errorf("an item links to %q, which is not a page of this site", item.Href)
		}
	}

	topic := layers["topic"]
	if topic.Tag != "Kürzung" {
		t.Errorf("tag = %q, want the kind named in the reader's language", topic.Tag)
	}
	if !strings.Contains(topic.Figure, "100 Mio. €") || !strings.HasPrefix(topic.Figure, "\u2212") {
		t.Errorf("figure = %q, want the amount as a loss with a real minus sign", topic.Figure)
	}
	// The totals are of everything, whatever the viewport.
	if !strings.Contains(answer.Status, "412") || !strings.Contains(answer.Status, "58") {
		t.Errorf("status = %q, want the totals in words", answer.Status)
	}

	// And in the other language the same data is worded differently — by the
	// server, with nothing for the script to translate.
	response = fetch(t, site, http.MethodGet, "/api/map?lang=en", false, "")
	if err := json.NewDecoder(response.Body).Decode(&answer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, item := range answer.Items {
		if item.Layer == "topic" && (item.Tag != "Cut" || !strings.Contains(item.Figure, "€100 million")) {
			t.Errorf("in English the topic reads %q / %q", item.Tag, item.Figure)
		}
	}
}
