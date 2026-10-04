package console

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestThePeoplePageIsAnAdministrators: drawn for them, and refused to anybody
// else — the backend refuses too, this only spares an editor a page that says
// no.
func TestThePeoplePageIsAnAdministrators(t *testing.T) {
	backend, _ := fakeBackend(t, false)

	_, asAdmin := get(t, served(t, backend.URL), "/collectives/c1")
	if !strings.Contains(asAdmin, `href="/settings/people"`) {
		t.Error("an administrator is not offered the people page — a page nothing links to is a page nobody finds")
	}
	_, asEditor := get(t, served(t, backend.URL, "schmerz-collective-buendnis-admins"), "/collectives/c1")
	if strings.Contains(asEditor, `href="/settings/people"`) {
		t.Error("an editor is offered the people page")
	}
	if response, _ := get(t, served(t, backend.URL, "schmerz-collective-buendnis-admins"), "/settings/people"); response.StatusCode != http.StatusForbidden {
		t.Errorf("an editor opening the people page = %d, want 403", response.StatusCode)
	}

	_, rendered := get(t, served(t, backend.URL), "/settings/people")
	// Your own row has no remove and no new link; the other one has both.
	if strings.Count(rendered, `value="remove"`) != 1 || strings.Count(rendered, `value="relink"`) != 1 {
		t.Error("the controls that would lock an administrator out are drawn on their own row")
	}
	if !strings.Contains(rendered, `class="warning"`) {
		t.Error("a directory with no recovery flow is not warned about")
	}
}

// TestAWayInIsShownOnceAndNeverInAnAddress: the link is somebody's account,
// so it is rendered into the answer to the form, never put in a redirect —
// where the browser's history and every log on the way would keep it.
func TestAWayInIsShownOnceAndNeverInAnAddress(t *testing.T) {
	backend, log := fakeBackend(t, false)
	console := served(t, backend.URL)

	response, rendered := post(t, console, "/settings/people", url.Values{
		"action": {"invite"}, "username": {"kai"}, "name": {"Kai"},
	})
	if response.StatusCode != http.StatusOK || response.Header.Get("Location") != "" {
		t.Fatalf("inviting = %d to %q, want the page itself", response.StatusCode, response.Header.Get("Location"))
	}
	if !strings.Contains(rendered, "https://auth.example/if/flow/recovery/?token=once") {
		t.Error("the way in is not shown")
	}

	sent, ok := log.last(http.MethodPost, "/v1/staff/people")
	if !ok || sent.Body["username"] != "kai" || sent.Body["admin"] != false {
		t.Fatalf("sent %v", sent.Body)
	}

	// Roles: a redirect, because nothing secret comes back.
	response, _ = post(t, console, "/settings/people", url.Values{
		"action": {"roles"}, "pk": {"8"}, "username": {"kai"}, "admin": {"1"},
	})
	if response.StatusCode != http.StatusSeeOther {
		t.Errorf("saving roles = %d, want a redirect", response.StatusCode)
	}
	if sent, _ := log.last(http.MethodPut, "/v1/staff/people/8"); sent.Body["admin"] != true || sent.Body["username"] != "kai" {
		t.Errorf("roles sent as %v", sent.Body)
	}
}

// TestARefusedInviteKeepsWhatWasTyped.
func TestARefusedInviteKeepsWhatWasTyped(t *testing.T) {
	backend, _ := fakeBackend(t, true)
	response, rendered := post(t, served(t, backend.URL), "/settings/people", url.Values{
		"action": {"invite"}, "username": {"jemand-neues"}, "name": {"Jemand Neues"}, "admin": {"1"},
		"email": {"jemand@example.org"},
	})
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", response.StatusCode)
	}
	for _, want := range []string{`value="jemand-neues"`, `value="Jemand Neues"`, `value="jemand@example.org"`} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the refused form lost %s", want)
		}
	}
}

// TestAnAdoptedAccountIsSaidSoAndGivenNoLink: they sign in with what they
// already have; a link minted unasked would let whoever held it replace it.
func TestAnAdoptedAccountIsSaidSoAndGivenNoLink(t *testing.T) {
	backend, _ := fakeBackend(t, false)
	response, rendered := post(t, served(t, backend.URL), "/settings/people#de", url.Values{
		"action": {"invite"}, "username": {"existing"}, "name": {"Jemand"}, "admin": {"1"},
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if !strings.Contains(rendered, "hatte schon ein Konto") {
		t.Error("the page does not say the account existed")
	}
	if strings.Contains(rendered, "/if/flow/recovery/") {
		t.Error("a way in was shown for an account that can already sign in")
	}
}

// TestAnEmailIsSentOnlyWhenItChanged: every row posts the address it shows,
// and an unchanged one is not a change to make in somebody's account.
func TestAnEmailIsSentOnlyWhenItChanged(t *testing.T) {
	backend, log := fakeBackend(t, false)
	console := served(t, backend.URL)

	post(t, console, "/settings/people", url.Values{
		"action": {"invite"}, "username": {"kai"}, "name": {"Kai"}, "email": {" kai@example.org "},
	})
	if sent, _ := log.last(http.MethodPost, "/v1/staff/people"); sent.Body["email"] != "kai@example.org" {
		t.Errorf("invited with email %v", sent.Body["email"])
	}

	unchanged := url.Values{"action": {"roles"}, "pk": {"8"}, "username": {"kai"},
		"email": {"kai@example.org"}, "email_was": {"kai@example.org"}}
	post(t, console, "/settings/people", unchanged)
	if _, sent := log.last(http.MethodPut, "/v1/staff/people/8/email"); sent {
		t.Error("an unchanged address was written to the account")
	}

	changed := url.Values{"action": {"roles"}, "pk": {"8"}, "username": {"kai"},
		"email": {"kai@neu.example"}, "email_was": {"kai@example.org"}}
	if response, _ := post(t, console, "/settings/people", changed); response.StatusCode != http.StatusSeeOther {
		t.Fatalf("saving = %d", response.StatusCode)
	}
	if sent, _ := log.last(http.MethodPut, "/v1/staff/people/8/email"); sent.Body["email"] != "kai@neu.example" {
		t.Errorf("email sent as %v", sent.Body)
	}

	// Shown where people are listed, as a way to write to them.
	_, rendered := get(t, console, "/collectives/c1")
	if !strings.Contains(rendered, `href="mailto:kai@example.org"`) {
		t.Error("a collective's people are not shown with their address")
	}
}
