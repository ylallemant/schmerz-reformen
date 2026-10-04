package console

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ylallemant/schmerz-reformen/internal/staffauth"
)

func testSealer(t *testing.T, secret string) *sealer {
	t.Helper()
	seal, _, err := newSealer(secret)
	if err != nil {
		t.Fatalf("newSealer: %v", err)
	}
	return seal
}

// TestASessionCookieCannotBeEdited is what the signature is for. Without it
// "which groups am I in" would be a question answered by the person asking.
func TestASessionCookieCannotBeEdited(t *testing.T) {
	seal := testSealer(t, "a configured secret")

	original := signedIn{
		Identity: staffauth.Identity{Subject: "sub-1", Name: "Dominique", Groups: []string{"duesseldorf"}},
		Expires:  time.Now().Add(time.Hour).Truncate(time.Second),
	}
	sealed, err := seal.seal(original)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	var opened signedIn
	if err := seal.open(sealed, &opened); err != nil {
		t.Fatalf("open: %v", err)
	}
	if opened.Identity.Subject != "sub-1" || len(opened.Identity.Groups) != 1 {
		t.Errorf("opened %+v, want what was sealed", opened)
	}

	// The same payload with the administrators' group added, keeping the
	// original signature: the edit an attacker would actually make.
	forged, err := seal.seal(signedIn{
		Identity: staffauth.Identity{Subject: "sub-1", Groups: []string{"duesseldorf", "schmerz-admins"}},
		Expires:  original.Expires,
	})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	forgedPayload, _, _ := strings.Cut(forged, ".")
	_, signature, _ := strings.Cut(sealed, ".")

	for name, value := range map[string]string{
		"an edited payload under the old signature": forgedPayload + "." + signature,
		"no signature":                 forgedPayload,
		"an empty signature":           forgedPayload + ".",
		"nothing":                      "",
		"not base64":                   "{not base64}.{neither}",
		"a truncated cookie":           sealed[:len(sealed)-4],
		"a cookie from another secret": mustSeal(t, testSealer(t, "another secret"), original),
	} {
		if err := seal.open(value, &signedIn{}); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func mustSeal(t *testing.T, seal *sealer, value any) string {
	t.Helper()
	sealed, err := seal.seal(value)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	return sealed
}

// TestWithNoSecretEachStartHasItsOwn: a random key is why everybody is signed
// out by a restart, and the console says so at startup. What must not happen
// is two starts sharing a key by accident — an all-zero one, say — which would
// make every cookie forgeable by anybody who read this file.
func TestWithNoSecretEachStartHasItsOwn(t *testing.T) {
	first, invented, err := newSealer("")
	if err != nil || !invented {
		t.Fatalf("newSealer(\"\") = invented %v, err %v", invented, err)
	}
	second, _, _ := newSealer("  ")

	sealed := mustSeal(t, first, signedIn{Identity: staffauth.Identity{Subject: "s"}})
	if err := second.open(sealed, &signedIn{}); err == nil {
		t.Error("two consoles started without a secret accept each other's cookies")
	}

	if _, invented, _ := newSealer("configured"); invented {
		t.Error("a configured secret was reported as invented")
	}
}

// TestAnExpiredSessionIsNoSession, whatever the browser chose to keep. The
// expiry is inside the signed payload because a cookie's own expiry is a
// request to the browser, not a property of the cookie.
func TestAnExpiredSessionIsNoSession(t *testing.T) {
	c := &console{sealer: testSealer(t, "secret")}

	request := func(session signedIn) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: mustSeal(t, c.sealer, session)})
		return r
	}
	who := staffauth.Identity{Subject: "sub-1", Name: "Dominique"}

	if _, ok := c.identityOf(request(signedIn{Identity: who, Expires: time.Now().Add(time.Hour)})); !ok {
		t.Error("a current session was refused")
	}
	if _, ok := c.identityOf(request(signedIn{Identity: who, Expires: time.Now().Add(-time.Minute)})); ok {
		t.Error("an expired session was accepted")
	}
	if _, ok := c.identityOf(request(signedIn{Expires: time.Now().Add(time.Hour)})); ok {
		t.Error("a session naming nobody was accepted")
	}
	if _, ok := c.identityOf(httptest.NewRequest(http.MethodGet, "/", nil)); ok {
		t.Error("a request with no cookie was treated as signed in")
	}
}

// TestEveryPageNeedsASignIn: the guard is on the whole port with its
// exceptions listed, because the page somebody forgets to guard is the one
// that needed it.
func TestEveryPageNeedsASignIn(t *testing.T) {
	// An identity provider is configured: what is being tested is what
	// needs a session, not the locked console (see
	// TestAConsoleWithNoIdentityProviderIsLocked).
	c := &console{sealer: testSealer(t, "secret"), signIn: &signIn{}}
	reached := false
	guarded := c.requireSignIn(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached = true
	}))

	for _, tc := range []struct {
		method, path string
		wantStatus   int
		wantReached  bool
	}{
		{http.MethodGet, "/", http.StatusSeeOther, false},
		{http.MethodGet, "/collectives/abc/topics", http.StatusSeeOther, false},
		{http.MethodGet, "/audit", http.StatusSeeOther, false},
		{http.MethodGet, "/settings/theme", http.StatusSeeOther, false},
		{http.MethodGet, "/api/themes", http.StatusSeeOther, false},
		// The theme library's download is under /theme/ and is not a public
		// asset: it is the console's.
		{http.MethodGet, "/theme/download/plakat", http.StatusSeeOther, false},
		// An uploaded logo is public on the public site, but this is the
		// console, and nothing here is anybody's but an editor's.
		{http.MethodGet, "/media/some-id", http.StatusSeeOther, false},
		// A write is refused, not redirected: a redirect would silently drop
		// what the editor typed.
		{http.MethodPost, "/collectives", http.StatusUnauthorized, false},
		{http.MethodPost, "/topics/abc/delete", http.StatusUnauthorized, false},
		{http.MethodPut, "/api/themes/active", http.StatusUnauthorized, false},

		// What has to be reachable before anybody has signed in.
		{http.MethodGet, "/auth/login", http.StatusOK, true},
		{http.MethodGet, "/auth/callback", http.StatusOK, true},
		{http.MethodGet, "/auth/signed-out", http.StatusOK, true},
		{http.MethodGet, "/static/base.css", http.StatusOK, true},
		{http.MethodGet, "/theme/tokens.css", http.StatusOK, true},
		{http.MethodGet, "/theme/assets/logo", http.StatusOK, true},
	} {
		reached = false
		recorder := httptest.NewRecorder()
		guarded.ServeHTTP(recorder, httptest.NewRequest(tc.method, tc.path, nil))

		if recorder.Code != tc.wantStatus || reached != tc.wantReached {
			t.Errorf("%s %s = %d (reached %v), want %d (reached %v)",
				tc.method, tc.path, recorder.Code, reached, tc.wantStatus, tc.wantReached)
		}
	}

	// And a redirect to sign in remembers where the editor was going.
	recorder := httptest.NewRecorder()
	guarded.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/topics/abc?notice=saved", nil))
	if location := recorder.Header().Get("Location"); !strings.Contains(location, "next=%2Ftopics%2Fabc") {
		t.Errorf("redirected to %q, want the destination carried along", location)
	}
}

// TestAfterSigningInYouStayOnThisSite: `next` arrives in a link anybody can
// write, and an editor who has just authenticated trusts the address bar.
func TestAfterSigningInYouStayOnThisSite(t *testing.T) {
	for next, want := range map[string]string{
		"/collectives/abc":          "/collectives/abc",
		"/topics/abc?notice=saved":  "/topics/abc?notice=saved",
		"":                          "/",
		"https://evil.example/":     "/",
		"//evil.example/":           "/",
		"/\\evil.example":           "/",
		"javascript:alert(1)":       "/",
		"collectives":               "/",
		"/auth/login?next=/":        "/", // not back into the sign-in itself
		"/auth/callback?code=stale": "/",
	} {
		if got := safeNext(next); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", next, got, want)
		}
	}
}

// TestARedirectKeepsItsFragment: the confirmation goes in the query, which has
// to come before the fragment, or the page scrolls nowhere and confirms
// nothing.
func TestARedirectKeepsItsFragment(t *testing.T) {
	for target, want := range map[string]string{
		"/collectives/abc":         "/collectives/abc?notice=saved",
		"/collectives/abc#members": "/collectives/abc?notice=saved#members",
		"/topics/abc?x=1#updates":  "/topics/abc?x=1&notice=saved#updates",
	} {
		recorder := httptest.NewRecorder()
		redirect(recorder, httptest.NewRequest(http.MethodPost, "/", nil), target, "saved")
		if got := recorder.Header().Get("Location"); got != want {
			t.Errorf("redirect(%q) went to %q, want %q", target, got, want)
		}
		if recorder.Code != http.StatusSeeOther {
			t.Errorf("redirect(%q) = %d, want 303 so a reload does not post again", target, recorder.Code)
		}
	}
}

// TestADevelopmentConsoleOffersSeveralEditors: one person testing locally has
// to be the author of a change and the three editors who approve it.
func TestADevelopmentConsoleOffersSeveralEditors(t *testing.T) {
	c := &console{development: true, developmentIdentity: staffauth.Identity{
		Subject: "development", Name: "Development", Groups: []string{"schmerz-admins"},
	}}

	first := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := c.standIn(first); got.Subject != "development" {
		t.Errorf("without a choice, subject = %q, want the configured identity", got.Subject)
	}

	third := httptest.NewRequest(http.MethodGet, "/", nil)
	third.AddCookie(&http.Cookie{Name: standInCookie, Value: "3"})
	got := c.standIn(third)
	if got.Subject != "development-3" || got.Name != "Development 3" || len(got.Groups) != 1 {
		t.Errorf("stand-in 3 = %+v, want another editor in the same groups", got)
	}

	for _, value := range []string{"0", "5", "x", "-1"} {
		odd := httptest.NewRequest(http.MethodGet, "/", nil)
		odd.AddCookie(&http.Cookie{Name: standInCookie, Value: value})
		if got := c.standIn(odd); got.Subject != "development" {
			t.Errorf("cookie %q gave %q, want the first stand-in", value, got.Subject)
		}
	}
}
