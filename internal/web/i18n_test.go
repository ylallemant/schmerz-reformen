package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
)

func testLocalization(t *testing.T) *Localization {
	t.Helper()
	catalogues := fstest.MapFS{
		"locales/active.de.toml": {Data: []byte(`"hello" = "Hallo"`)},
		"locales/active.en.toml": {Data: []byte(`"hello" = "Hello"`)},
	}
	localization, err := NewLocalization(catalogues, "locales", "de")
	if err != nil {
		t.Fatalf("NewLocalization: %v", err)
	}
	return localization
}

// resolved runs a request through the middleware and reports the language
// the page would be rendered in.
func resolved(t *testing.T, l *Localization, request *http.Request) string {
	t.Helper()
	var lang string
	l.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		lang = LanguageFrom(r.Context())
	})).ServeHTTP(httptest.NewRecorder(), request)
	return lang
}

// TestTheChoiceWinsThenTheBrowserThenTheFallback.
func TestTheChoiceWinsThenTheBrowserThenTheFallback(t *testing.T) {
	l := testLocalization(t)

	for name, tc := range map[string]struct {
		cookie, accept, want string
	}{
		"nothing at all":                            {"", "", "de"},
		"a browser in English":                      {"", "en-GB,en;q=0.9", "en"},
		"a browser in a language not had":           {"", "fr-FR,fr;q=0.9", "de"},
		"a browser preferring French, then English": {"", "fr,en;q=0.5", "en"},
		"a choice over the browser":                 {"de", "en-GB", "de"},
		"a choice nobody offers":                    {"fr", "en", "en"},
	} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		if tc.cookie != "" {
			request.AddCookie(&http.Cookie{Name: LanguageCookie, Value: tc.cookie})
		}
		if tc.accept != "" {
			request.Header.Set("Accept-Language", tc.accept)
		}
		if got := resolved(t, l, request); got != tc.want {
			t.Errorf("%s: language = %q, want %q", name, got, tc.want)
		}
	}
}

// TestTheAddressNeverCarriesALanguage: a link somebody sends is read in the
// receiver's language, and the middleware sets nothing because of a query.
func TestTheAddressNeverCarriesALanguage(t *testing.T) {
	l := testLocalization(t)

	request := httptest.NewRequest(http.MethodGet, "/?lang=en", nil)
	request.Header.Set("Accept-Language", "de-DE")

	recorder := httptest.NewRecorder()
	var lang string
	l.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		lang = LanguageFrom(r.Context())
	})).ServeHTTP(recorder, request)

	if lang != "de" {
		t.Errorf("language = %q: a query parameter chose the language", lang)
	}
	if cookies := recorder.Result().Cookies(); len(cookies) != 0 {
		t.Errorf("reading a page set %d cookies", len(cookies))
	}
}

// TestTheSwitcherWorksWithoutTheScript: the form sets the cookie the script
// would have set, and sends the reader back where they were.
func TestTheSwitcherWorksWithoutTheScript(t *testing.T) {
	l := testLocalization(t)

	post := func(form url.Values) *http.Response {
		request := httptest.NewRequest(http.MethodPost, "/language", strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		recorder := httptest.NewRecorder()
		l.Switch(recorder, request)
		return recorder.Result()
	}

	response := post(url.Values{"lang": {"en"}, "next": {"/calendar?month=2026-10"}})
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/calendar?month=2026-10" {
		t.Errorf("went %d to %q, want back to the page", response.StatusCode, response.Header.Get("Location"))
	}
	cookies := response.Cookies()
	if len(cookies) != 1 || cookies[0].Name != LanguageCookie || cookies[0].Value != "en" || cookies[0].MaxAge <= 0 {
		t.Fatalf("cookies = %+v, want the choice remembered", cookies)
	}
	// The script has to be able to write and clear it.
	if cookies[0].HttpOnly {
		t.Error("the language cookie is HttpOnly, so the script cannot keep it in step")
	}

	// An unknown language clears the choice: the browser decides again.
	response = post(url.Values{"lang": {"xx"}, "next": {"/"}})
	if cookies := response.Cookies(); len(cookies) != 1 || cookies[0].MaxAge >= 0 {
		t.Errorf("cookies = %+v, want the choice cleared", cookies)
	}

	// And the way back is held to this site.
	response = post(url.Values{"lang": {"de"}, "next": {"https://evil.example/"}})
	if got := response.Header.Get("Location"); got != "/" {
		t.Errorf("went to %q, want this site's front page", got)
	}
}
