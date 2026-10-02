package backend

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/ylallemant/schmerz-reformen/internal/config"
	"github.com/ylallemant/schmerz-reformen/internal/models"
	"github.com/ylallemant/schmerz-reformen/internal/staffauth"
	"github.com/ylallemant/schmerz-reformen/internal/store"
)

// served puts a backend behind a real HTTP handler, middleware included.
//
// The tests in this file go through HTTP on purpose. The rules they pin live
// in middleware — who is believed to be the console, and whose routes are an
// administrator's — and a test that called a handler directly would walk
// straight past every one of them.
func served(t *testing.T, a *API) http.Handler {
	t.Helper()

	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("schMERZ-Reformen", "test"))
	api.UseMiddleware(a.authenticate(api))
	registerEverything(a, api)
	return mux
}

// call makes one request and returns the status and the decoded body.
func call(t *testing.T, handler http.Handler, method, path string, headers map[string]string, body any) (int, map[string]any) {
	t.Helper()

	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}

	request := httptest.NewRequest(method, path, &payload)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	var decoded map[string]any
	json.Unmarshal(recorder.Body.Bytes(), &decoded) //nolint:errcheck // a non-JSON body is simply nil
	return recorder.Code, decoded
}

// identityHeader renders who the console says is acting.
func identityHeader(t *testing.T, subject string, groups ...string) string {
	t.Helper()

	encoded, err := staffauth.Identity{Subject: subject, Name: subject, Groups: groups}.Encode()
	if err != nil {
		t.Fatalf("encode identity: %v", err)
	}
	return encoded
}

// TestTheConsoleIsBelievedOnlyWithItsSecret.
//
// The frontend reaches this service too, on the same network, and it is the
// one the whole internet talks to. Without the token, a bug there that let a
// reader choose a header would be a reader publishing as an editor.
func TestTheConsoleIsBelievedOnlyWithItsSecret(t *testing.T) {
	a := newAPI(t)
	a.staffToken = "the-shared-secret"
	handler := served(t, a)

	adminIdentity := identityHeader(t, "sub-admin", config.DefaultAdminGroup)

	for name, tc := range map[string]struct {
		headers map[string]string
		want    int
	}{
		"no headers at all": {nil, http.StatusUnauthorized},
		"an identity and no secret": {
			map[string]string{staffauth.IdentityHeader: adminIdentity},
			http.StatusUnauthorized,
		},
		"an identity and the wrong secret": {
			map[string]string{staffauth.IdentityHeader: adminIdentity, staffauth.TokenHeader: "a guess"},
			http.StatusUnauthorized,
		},
		"the secret and nobody acting": {
			map[string]string{staffauth.TokenHeader: "the-shared-secret"},
			http.StatusUnauthorized,
		},
		"the secret and an identity with no subject": {
			map[string]string{
				staffauth.TokenHeader:    "the-shared-secret",
				staffauth.IdentityHeader: identityHeader(t, "", config.DefaultAdminGroup),
			},
			http.StatusUnauthorized,
		},
		"the secret and an identity that is not base64": {
			map[string]string{staffauth.TokenHeader: "the-shared-secret", staffauth.IdentityHeader: "{not json}"},
			http.StatusUnauthorized,
		},
		"the secret and an administrator": {
			map[string]string{staffauth.TokenHeader: "the-shared-secret", staffauth.IdentityHeader: adminIdentity},
			http.StatusOK,
		},
	} {
		t.Run(name, func(t *testing.T) {
			status, _ := call(t, handler, http.MethodGet, "/v1/staff/me", tc.headers, nil)
			if status != tc.want {
				t.Errorf("status = %d, want %d", status, tc.want)
			}
		})
	}
}

// TestWithNoSecretTheContentAPIIsOff: nothing could make an identity
// believable, so nothing is believed — and the answer says it is a
// configuration problem rather than the caller's.
func TestWithNoSecretTheContentAPIIsOff(t *testing.T) {
	a := newAPI(t)
	handler := served(t, a)

	headers := map[string]string{
		staffauth.IdentityHeader: identityHeader(t, "sub-admin", config.DefaultAdminGroup),
	}
	status, _ := call(t, handler, http.MethodGet, "/v1/staff/me", headers, nil)
	if status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 when no staff token is configured", status)
	}

	// The public half is untouched: readers can still read.
	status, _ = call(t, handler, http.MethodGet, "/v1/collectives", nil, nil)
	if status != http.StatusOK {
		t.Errorf("public listing = %d, want 200: a missing console setting must not take the site down", status)
	}
}

// TestDevelopmentBelievesTheIdentityWithoutASecret — and a secret that is
// configured is enforced even then, so a development flag left on by mistake
// does not also open the content API.
func TestDevelopmentBelievesTheIdentityWithoutASecret(t *testing.T) {
	a := newAPI(t)
	a.development = true
	handler := served(t, a)

	headers := map[string]string{
		staffauth.IdentityHeader: identityHeader(t, "development", config.DefaultAdminGroup),
	}
	status, body := call(t, handler, http.MethodGet, "/v1/staff/me", headers, nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 in development", status)
	}
	if body["admin"] != true {
		t.Errorf("admin = %v, want the development identity to carry the admin group it sent", body["admin"])
	}

	// Still needs to say who: development removes the secret, not the name in
	// the audit log.
	status, _ = call(t, handler, http.MethodGet, "/v1/staff/me", nil, nil)
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d with no identity, want 401 even in development", status)
	}

	a.staffToken = "configured-anyway"
	status, _ = call(t, handler, http.MethodGet, "/v1/staff/me", headers, nil)
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401: a configured secret is enforced whatever the flag says", status)
	}
}

// TestAReaderIsNeverTheConsole: a session token, however valid, opens no
// console route. The two identities are different systems and one must not
// promote into the other.
func TestAReaderIsNeverTheConsole(t *testing.T) {
	a := newAPI(t)
	a.staffToken = "the-shared-secret"
	handler := served(t, a)

	status, _ := call(t, handler, http.MethodPost, "/v1/staff/collectives",
		map[string]string{"Authorization": "Bearer a-readers-session-token"},
		map[string]any{"name": "Mine now"})
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", status)
	}

	if _, total, _ := a.store.ListCollectives(t.Context(), store.CollectiveQuery{}); total != 0 {
		t.Error("a collective was created by somebody who is not the console")
	}
}

// TestTheThemeIsAnAdministrators, and changing it is written down without the
// handler doing anything.
func TestTheThemeIsAnAdministrators(t *testing.T) {
	a := newAPI(t)
	a.staffToken = "the-shared-secret"
	handler := served(t, a)

	asEditor := map[string]string{
		staffauth.TokenHeader:    "the-shared-secret",
		staffauth.IdentityHeader: identityHeader(t, "sub-editor", "duesseldorf"),
	}
	asAdmin := map[string]string{
		staffauth.TokenHeader:    "the-shared-secret",
		staffauth.IdentityHeader: identityHeader(t, "sub-admin", config.DefaultAdminGroup),
	}
	selection := map[string]any{"name": "built-in"}

	status, _ := call(t, handler, http.MethodPut, "/v1/themes/active", asEditor, selection)
	if status != http.StatusForbidden {
		t.Errorf("an editor changing the theme = %d, want 403", status)
	}
	if _, total, _ := a.store.ListAudit(t.Context(), store.AuditQuery{}); total != 0 {
		t.Error("a refused change was written to the audit log")
	}

	status, _ = call(t, handler, http.MethodPut, "/v1/themes/active", asAdmin, selection)
	if status != http.StatusNoContent && status != http.StatusOK {
		t.Fatalf("an administrator changing the theme = %d, want success", status)
	}

	entries, total, err := a.store.ListAudit(t.Context(), store.AuditQuery{})
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	if total != 1 {
		t.Fatalf("%d audit entries, want the one change", total)
	}
	if entries[0].Actor != "sub-admin" || entries[0].Action != models.AuditConfigChange {
		t.Errorf("entry = %+v, want the administrator named against a configuration change", entries[0])
	}

	// Reading the library is not a change and leaves no entry.
	status, _ = call(t, handler, http.MethodGet, "/v1/themes", asAdmin, nil)
	if status != http.StatusOK {
		t.Errorf("listing themes = %d, want 200", status)
	}
	if _, total, _ := a.store.ListAudit(t.Context(), store.AuditQuery{}); total != 1 {
		t.Error("reading the theme library was written to the audit log")
	}
}

// TestACollectiveWithNoGroupIsNobodysButAnAdministrators.
//
// It is the state a collective is created in. An empty group matching an
// editor who is in no groups is exactly the accident worth a test.
func TestACollectiveWithNoGroupIsNobodysButAnAdministrators(t *testing.T) {
	ungrouped := models.Collective{AuthGroup: ""}
	grouped := models.Collective{AuthGroup: "duesseldorf"}

	nobody := &staff{Identity: staffauth.Identity{Subject: "s"}}
	blank := &staff{Identity: staffauth.Identity{Subject: "s", Groups: []string{""}}}
	member := &staff{Identity: staffauth.Identity{Subject: "s", Groups: []string{"duesseldorf"}}}
	other := &staff{Identity: staffauth.Identity{Subject: "s", Groups: []string{"koeln"}}}
	administrator := &staff{Identity: staffauth.Identity{Subject: "s"}, Admin: true}

	for name, tc := range map[string]struct {
		who        *staff
		collective models.Collective
		want       bool
	}{
		"in no group, collective with no group":      {nobody, ungrouped, false},
		"in a blank group, collective with no group": {blank, ungrouped, false},
		"in the group":                                 {member, grouped, true},
		"in another group":                             {other, grouped, false},
		"in no group, collective with a group":         {nobody, grouped, false},
		"an administrator, collective with no group":   {administrator, ungrouped, true},
		"an administrator, somebody else's collective": {administrator, grouped, true},
	} {
		if got := tc.who.manages(tc.collective); got != tc.want {
			t.Errorf("%s: manages = %v, want %v", name, got, tc.want)
		}
	}
}
