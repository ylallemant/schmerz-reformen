package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ylallemant/schmerz-reformen/internal/models"
	"github.com/ylallemant/schmerz-reformen/internal/staffauth"
)

// provisioned runs the setup wizard's request against a fake directory and
// returns what it answered.
func provisioned(t *testing.T, a *API, f *fakeDirectory) ProvisionItem {
	t.Helper()
	in := &ProvisionInput{}
	in.Body.InstanceURL = f.URL()
	in.Body.Token = "a-pasted-setup-token"
	in.Body.ConsoleURL = "https://console.example/"
	in.Body.AdminUsername = "maria"
	in.Body.AdminName = "Maria"
	out, err := a.consoleProvisionAuth(context.Background(), in)
	if err != nil {
		t.Fatalf("consoleProvisionAuth: %v", err)
	}
	return out.Body
}

// TestProvisioningMakesTheSiteAnswerToTheDirectory: the wizard's one request
// leaves a provider that can be verified, an administrators' group with the
// named first admin in it, a recovery flow, a token of the backend's own — and
// a backend that now reads editors' groups from there.
func TestProvisioningMakesTheSiteAnswerToTheDirectory(t *testing.T) {
	a := newAPI(t)
	f := newFakeDirectory(t)

	got := provisioned(t, a, f)
	if !got.Done || got.AdminUsername != "maria" || got.TokenOwner != "akadmin" {
		t.Fatalf("result = %+v", got)
	}
	if got.ServiceAccount != "schmerz-service" || got.ServiceAccountNote != "" {
		t.Errorf("service account %q (%q), want the site's own", got.ServiceAccount, got.ServiceAccountNote)
	}
	if !f.usedBearer("key-of-schmerz-service-api") {
		t.Error("the service account's token was stored without being tried")
	}
	f.mu.Lock()
	granted := f.assigned["role-schmerz-service"]
	bound := f.groupRoles["uuid-schmerz-service"]
	f.mu.Unlock()
	if len(granted) == 0 || len(bound) != 1 {
		t.Errorf("the service role holds %v and is bound to %v", granted, bound)
	}
	if !slices.Contains(f.groupsOf("schmerz-service"), "schmerz-service") {
		t.Errorf("the service account is in %v, want its group", f.groupsOf("schmerz-service"))
	}
	if token := f.tokens["schmerz-service-api"]; token == nil || token["intent"] != "api" || token["expiring"] != false {
		t.Errorf("the service account's token = %v, want a non-expiring api token", token)
	}
	if got.AdminLink == "" || !got.RecoveryReady || got.OwnToken != "schmerz-console" {
		t.Errorf("link %q, recovery %v, own token %q", got.AdminLink, got.RecoveryReady, got.OwnToken)
	}

	provider := f.body("POST /providers/oauth2/")
	if provider["signing_key"] != "cert-1" {
		t.Errorf("provider signing_key = %v: without one Authentik signs HS256 and nothing can verify it", provider["signing_key"])
	}
	uris, _ := provider["redirect_uris"].([]any)
	if len(uris) != 1 || uris[0].(map[string]any)["url"] != "https://console.example/auth/callback" ||
		uris[0].(map[string]any)["matching_mode"] != "strict" {
		t.Errorf("redirect_uris = %v", provider["redirect_uris"])
	}
	if mappings, _ := provider["property_mappings"].([]any); slices.Contains(mappings, any("scope-email")) {
		t.Error("the provider asks for email: the console has no use for an editor's address")
	}
	if token := f.body("POST /core/tokens/"); token["expiring"] != false || token["intent"] != "api" {
		t.Errorf("the backend's own token = %v: it must not expire", token)
	}
	if write := f.body("POST /stages/user_write/"); write["user_creation_mode"] != "never_create" {
		t.Errorf("recovery write stage = %v: a recovery flow must never create accounts", write)
	}
	if !slices.Contains(f.groupsOf("maria"), "schmerz-admins") {
		t.Errorf("maria is in %v, want the administrators' group", f.groupsOf("maria"))
	}

	stored, err := a.store.AuthSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Provisioned || stored.APIToken != "key-of-schmerz-service-api" || stored.ClientSecret == "" {
		t.Errorf("stored = provisioned %v, token %q: the backend works as its own service account, "+
			"never with the pasted setup token", stored.Provisioned, stored.APIToken)
	}
	if a.currentAdminGroup() != "schmerz-admins" || !a.provisioned.Load() {
		t.Errorf("admin group %q, provisioned %v", a.currentAdminGroup(), a.provisioned.Load())
	}

	// Again, without saying it means to replace it.
	in := &ProvisionInput{}
	in.Body.InstanceURL, in.Body.Token, in.Body.ConsoleURL = f.URL(), "x", "https://console.example"
	if _, err := a.consoleProvisionAuth(context.Background(), in); statusOf(err) != http.StatusConflict {
		t.Errorf("provisioning twice: status %d, want 409", statusOf(err))
	}
}

// TestAnInstanceThatCannotSignIsReportedNotHalfProvisioned: what a provider
// needs is checked before anything is created.
func TestAnInstanceThatCannotSignIsReportedNotHalfProvisioned(t *testing.T) {
	a := newAPI(t)
	f := newFakeDirectory(t)
	f.hasCert = false

	got := provisioned(t, a, f)
	if got.Done || len(got.Missing) == 0 {
		t.Fatalf("result = %+v, want not done with what is missing", got)
	}
	if f.called("POST /providers/") != 0 || f.called("POST /core/applications/") != 0 {
		t.Error("something was created although the instance cannot sign tokens")
	}
	if done, _ := a.store.AuthProvisioned(context.Background()); done {
		t.Error("an unfinished provisioning was stored")
	}
}

// TestTheConsolesOwnRoutesNeedItsSecret, and nobody signed in.
func TestTheConsolesOwnRoutesNeedItsSecret(t *testing.T) {
	a := newAPI(t)
	a.staffToken = "the-shared-secret"
	handler := served(t, a)

	if status, _ := call(t, handler, http.MethodGet, "/v1/console/auth", nil, nil); status != http.StatusUnauthorized {
		t.Errorf("without the secret: %d", status)
	}
	status, body := call(t, handler, http.MethodGet, "/v1/console/auth",
		map[string]string{staffauth.TokenHeader: "the-shared-secret"}, nil)
	if status != http.StatusOK || body["provisioned"] != false {
		t.Errorf("with it: %d %v", status, body)
	}
	if status, _ := call(t, handler, http.MethodGet, "/v1/console/auth/credentials",
		map[string]string{staffauth.TokenHeader: "the-shared-secret"}, nil); status != http.StatusNotFound {
		t.Errorf("credentials before provisioning: %d, want 404", status)
	}

	provisioned(t, a, newFakeDirectory(t))
	status, body = call(t, handler, http.MethodGet, "/v1/console/auth/credentials",
		map[string]string{staffauth.TokenHeader: "the-shared-secret"}, nil)
	if status != http.StatusOK || body["client_secret"] != "secret-of-the-provider" {
		t.Errorf("credentials: %d %v", status, body)
	}
	if _, leaked := body["api_token"]; leaked {
		t.Error("the directory token left the backend")
	}
	status, body = call(t, handler, http.MethodGet, "/v1/console/auth",
		map[string]string{staffauth.TokenHeader: "the-shared-secret"}, nil)
	if body["issuer"] == "" || body["admin_group_name"] != "schmerz-admins" {
		t.Errorf("settings: %d %v", status, body)
	}
}

// editorHeaders is the console speaking for somebody: its secret, and an
// identity whose groups say whatever the test wants them to claim.
func editorHeaders(t *testing.T, username string, claimed ...string) map[string]string {
	t.Helper()
	encoded, err := staffauth.Identity{Subject: "sub-" + username, Name: username, Username: username, Groups: claimed}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{staffauth.TokenHeader: "the-shared-secret", staffauth.IdentityHeader: encoded}
}

// TestGroupsComeFromTheDirectoryNotTheHeader: once provisioned, the console's
// word about somebody's groups counts for nothing. A read may lean on what the
// directory said a minute ago; a write asks again.
func TestGroupsComeFromTheDirectoryNotTheHeader(t *testing.T) {
	a := newAPI(t)
	a.staffToken = "the-shared-secret"
	f := newFakeDirectory(t)
	provisioned(t, a, f)
	f.addUser("kai", "buendnis-koeln")
	handler := served(t, a)

	// Kai claims to administer. The directory says otherwise.
	status, body := call(t, handler, http.MethodGet, "/v1/staff/me",
		editorHeaders(t, "kai", "schmerz-admins"), nil)
	if status != http.StatusOK || body["admin"] != false {
		t.Fatalf("kai claiming the admin group: %d %v", status, body)
	}
	status, _ = call(t, handler, http.MethodPost, "/v1/staff/collectives",
		editorHeaders(t, "kai", "schmerz-admins"), map[string]any{"name": "Neues Bündnis"})
	if status != http.StatusForbidden {
		t.Errorf("kai creating a collective: %d, want 403", status)
	}

	// Made an administrator in Authentik: a write sees it at once.
	f.setGroups("kai", "schmerz-admins")
	status, _ = call(t, handler, http.MethodPost, "/v1/staff/collectives",
		editorHeaders(t, "kai"), map[string]any{"name": "Neues Bündnis"})
	if status != http.StatusOK {
		t.Errorf("kai, now an administrator in the directory, creating a collective: %d", status)
	}
	// And its two groups were made in the directory, so people can be put
	// in them.
	f.mu.Lock()
	_, admins := f.groups["uuid-schmerz-collective-neues-buendnis-admins"]
	_, authors := f.groups["uuid-schmerz-collective-neues-buendnis-authors"]
	f.mu.Unlock()
	if !admins || !authors {
		t.Error("the new collective's groups were not created in the directory")
	}

	// Somebody the directory does not know is nobody.
	status, body = call(t, handler, http.MethodGet, "/v1/staff/me",
		editorHeaders(t, "ghost", "schmerz-admins"), nil)
	if status != http.StatusOK || body["admin"] != false || len(body["collectives"].([]any)) != 0 {
		t.Errorf("an unknown account: %d %v", status, body)
	}

	// An identity with no username cannot be looked up, and is not believed.
	encoded, _ := staffauth.Identity{Subject: "s", Groups: []string{"schmerz-admins"}}.Encode()
	status, _ = call(t, handler, http.MethodGet, "/v1/staff/me",
		map[string]string{staffauth.TokenHeader: "the-shared-secret", staffauth.IdentityHeader: encoded}, nil)
	if status != http.StatusUnauthorized {
		t.Errorf("no username: %d, want 401", status)
	}
}

// TestAReadMayLeanOnTheCacheAWriteMayNot.
func TestAReadMayLeanOnTheCacheAWriteMayNot(t *testing.T) {
	a := newAPI(t)
	a.staffToken = "the-shared-secret"
	f := newFakeDirectory(t)
	provisioned(t, a, f)
	handler := served(t, a)

	if status, body := call(t, handler, http.MethodGet, "/v1/staff/me", editorHeaders(t, "maria"), nil); status != http.StatusOK || body["admin"] != true {
		t.Fatalf("maria: %d %v", status, body)
	}
	before := f.called("GET /core/users/")
	call(t, handler, http.MethodGet, "/v1/staff/me", editorHeaders(t, "maria"), nil)
	if f.called("GET /core/users/") != before {
		t.Error("a second read within a minute asked the directory again")
	}

	// Taken out of the administrators in Authentik: the next write refuses.
	f.setGroups("maria")
	status, _ := call(t, handler, http.MethodPost, "/v1/staff/collectives",
		editorHeaders(t, "maria"), map[string]any{"name": "Zu spät"})
	if status != http.StatusForbidden {
		t.Errorf("a write after being removed: %d, want 403", status)
	}

	// And the cache ages out for reads too.
	a.directory.cacheMu.Lock()
	for name, cached := range a.directory.cache {
		cached.at = time.Now().Add(-2 * roleCacheLifetime)
		a.directory.cache[name] = cached
	}
	a.directory.cacheMu.Unlock()
	if _, body := call(t, handler, http.MethodGet, "/v1/staff/me", editorHeaders(t, "maria"), nil); body["admin"] != false {
		t.Errorf("a read after the cache aged out still says admin: %v", body)
	}
}

// TestARevokedTokenIsTheOperatorsProblem: said as unavailable, not as the
// editor having done something wrong — nothing they send can fix it.
func TestARevokedTokenIsTheOperatorsProblem(t *testing.T) {
	a := newAPI(t)
	a.staffToken = "the-shared-secret"
	f := newFakeDirectory(t)
	provisioned(t, a, f)
	handler := served(t, a)

	f.mu.Lock()
	f.refuseToken = true
	f.mu.Unlock()
	status, body := call(t, handler, http.MethodPost, "/v1/staff/collectives",
		editorHeaders(t, "maria"), map[string]any{"name": "x"})
	if status != http.StatusServiceUnavailable {
		t.Errorf("status = %d (%v), want 503", status, body)
	}
}

// TestDevelopmentStandInsAreStillBelieved: a local run's stand-ins carry no
// username, and are what lets one person be several editors there.
func TestDevelopmentStandInsAreStillBelieved(t *testing.T) {
	a := newAPI(t)
	a.development = true
	provisioned(t, a, newFakeDirectory(t))
	handler := served(t, a)

	encoded, _ := staffauth.Identity{Subject: "development-2", Groups: []string{"schmerz-admins"}}.Encode()
	status, body := call(t, handler, http.MethodGet, "/v1/staff/me",
		map[string]string{staffauth.IdentityHeader: encoded}, nil)
	if status != http.StatusOK || body["admin"] != true {
		t.Errorf("a stand-in: %d %v", status, body)
	}
}

// TestPeopleAreManagedInTheDirectory: added as users with a way in, made
// administrators and not, never left without an administrator, and listed
// with every role they hold.
func TestPeopleAreManagedInTheDirectory(t *testing.T) {
	a := newAPI(t)
	f := newFakeDirectory(t)
	provisioned(t, a, f)
	maria := asStaff("sub-maria", "Maria", "schmerz-admins")
	maria.Value(staffKey).(*staff).Username = "maria"

	invite := &InviteInput{}
	invite.Body.Username, invite.Body.Name = "kai", "Kai"
	added, err := a.staffInvitePerson(maria, invite)
	if err != nil {
		t.Fatalf("staffInvitePerson: %v", err)
	}
	if added.Body.Link == "" || !slices.Equal(f.groupsOf("kai"), []string{"schmerz-users"}) {
		t.Errorf("kai: link %q, groups %v — a new person is a user", added.Body.Link, f.groupsOf("kai"))
	}
	if created := f.body("POST /core/users/"); created["password"] != nil {
		t.Error("a password was set: the way in is a link the person sets up themselves")
	}

	people, err := a.staffListPeople(maria, nil)
	if err != nil {
		t.Fatalf("staffListPeople: %v", err)
	}
	var kaiPK, mariaPK int
	for _, person := range people.Body.People {
		switch person.Username {
		case "kai":
			kaiPK = person.PK
		case "maria":
			mariaPK = person.PK
			if !person.Admin || !person.Self {
				t.Errorf("maria listed as %+v", person)
			}
		}
	}
	if kaiPK == 0 || mariaPK == 0 {
		t.Fatalf("people = %+v", people.Body.People)
	}

	stepDown := &SetAdminInput{PK: mariaPK}
	stepDown.Body.Username = "maria"
	if _, err := a.staffSetPersonAdmin(maria, stepDown); statusOf(err) != http.StatusConflict {
		t.Errorf("the last administrator stepping down: status %d, want 409", statusOf(err))
	}
	if _, err := a.staffRemovePerson(maria, &PersonInput{PK: mariaPK, Username: "maria"}); statusOf(err) != http.StatusConflict {
		t.Errorf("removing yourself: status %d, want 409", statusOf(err))
	}

	promote := &SetAdminInput{PK: kaiPK}
	promote.Body.Username, promote.Body.Admin = "kai", true
	if _, err := a.staffSetPersonAdmin(maria, promote); err != nil {
		t.Fatalf("promote kai: %v", err)
	}
	if _, err := a.staffRemovePerson(maria, &PersonInput{PK: mariaPK, Username: "maria"}); statusOf(err) != http.StatusConflict {
		t.Errorf("removing yourself beside another administrator: status %d, want 409", statusOf(err))
	}
	if _, err := a.staffSetPersonAdmin(maria, stepDown); err != nil {
		t.Errorf("maria stepping down beside another administrator: %v", err)
	}
	if groups := f.groupsOf("maria"); slices.Contains(groups, "schmerz-admins") || !slices.Contains(groups, "schmerz-users") {
		t.Errorf("maria after stepping down is in %v, want a user still", groups)
	}

	// Removing somebody takes them out of every group of this site — the
	// per-entry ones too — and leaves the account.
	koeln := founded(t, a, "Bündnis Köln", "koeln")
	kai := asStaff("sub-kai", "Kai", "schmerz-admins")
	if _, err := a.staffCollectiveGrant(kai, &EntryRoleInput{ID: koeln.ID, Role: "authors", PK: mariaPK, Username: "maria"}); err != nil {
		t.Fatalf("staffCollectiveGrant: %v", err)
	}
	listed, _ := a.staffListPeople(kai, nil)
	for _, person := range listed.Body.People {
		if person.Username == "maria" && (len(person.Roles) != 1 || person.Roles[0].Role != "author") {
			t.Errorf("maria's roles = %+v, want author of Köln", person.Roles)
		}
	}
	if _, err := a.staffRemovePerson(kai, &PersonInput{PK: mariaPK, Username: "maria"}); err != nil {
		t.Fatalf("staffRemovePerson: %v", err)
	}
	if groups := f.groupsOf("maria"); len(groups) != 0 {
		t.Errorf("maria is still in %v", groups)
	}
	if f.called("DELETE /core/users/") != 0 {
		t.Error("an account was deleted from the operator's directory")
	}
}

// TestEachRoleIsGivenByWhomItShouldBe: a collective's administrators choose
// its authors, not its administrators; an organisation's administrators
// choose its people; nobody else chooses anything.
func TestEachRoleIsGivenByWhomItShouldBe(t *testing.T) {
	a := newAPI(t)
	f := newFakeDirectory(t)
	provisioned(t, a, f)
	koeln := founded(t, a, "Bündnis Köln", "koeln")
	kai := f.addUser("kai", "schmerz-users")
	collectiveAdmin := editor("Lea", adminsOf("koeln"))
	author := editor("Sam", authorsOf("koeln"))

	grant := func(ctx context.Context, role string) int {
		_, err := a.staffCollectiveGrant(ctx, &EntryRoleInput{ID: koeln.ID, Role: role, PK: kai.PK, Username: "kai"})
		return statusOf(err)
	}
	if status := grant(collectiveAdmin, "authors"); status != 0 {
		t.Errorf("a collective's administrator choosing an author: status %d", status)
	}
	if !slices.Contains(f.groupsOf("kai"), authorsOf("koeln")) {
		t.Errorf("kai is in %v", f.groupsOf("kai"))
	}
	if status := grant(collectiveAdmin, "admins"); status != http.StatusForbidden {
		t.Errorf("a collective's administrator choosing another: status %d, want 403", status)
	}
	if status := grant(author, "authors"); status != http.StatusForbidden {
		t.Errorf("an author choosing an author: status %d, want 403", status)
	}
	if status := grant(admin(), "admins"); status != 0 {
		t.Errorf("the site's administrator choosing a collective's: status %d", status)
	}

	people, err := a.staffCollectivePeople(collectiveAdmin, &CollectiveIDInput{ID: koeln.ID})
	if err != nil || len(people.Body.Admins) != 1 || len(people.Body.Others) != 1 || people.Body.MayGrantAdmins {
		t.Errorf("collective people = %+v, %v", people, err)
	}

	// An organisation, created by the site's administrator, run by its own.
	create := &CreateOrganisationInput{}
	create.Body.Name, create.Body.Kind = "ver.di Köln", "union"
	organisation, err := a.staffCreateOrganisation(admin(), create)
	if err != nil {
		t.Fatalf("staffCreateOrganisation: %v", err)
	}
	if organisation.Body.AdminGroup != "schmerz-organisation-ver-di-koeln-admins" {
		t.Errorf("organisation admins = %q", organisation.Body.AdminGroup)
	}
	f.mu.Lock()
	_, made := f.groups["uuid-schmerz-organisation-ver-di-koeln-members"]
	f.mu.Unlock()
	if !made {
		t.Error("the organisation's groups were not created in the directory")
	}
	if _, err := a.staffCreateOrganisation(collectiveAdmin, create); statusOf(err) != http.StatusForbidden {
		t.Errorf("a collective's administrator creating an organisation: status %d", statusOf(err))
	}

	orgAdmin := editor("Ola", organisation.Body.AdminGroup)
	if _, err := a.staffOrganisationGrant(orgAdmin, &EntryRoleInput{ID: organisation.Body.ID, Role: "members", PK: kai.PK, Username: "kai"}); err != nil {
		t.Errorf("an organisation's administrator adding a member: %v", err)
	}
	// From the console's picker, which sends only the key: the username is
	// looked up, so the change reaches their cached roles at once.
	ola := f.addUser("ola", "schmerz-users")
	if _, err := a.staffOrganisationGrant(orgAdmin, &EntryRoleInput{ID: organisation.Body.ID, Role: "admins", PK: ola.PK}); err != nil {
		t.Errorf("giving a role by key alone: %v", err)
	}
	if !slices.Contains(f.groupsOf("ola"), organisation.Body.AdminGroup) {
		t.Errorf("ola is in %v", f.groupsOf("ola"))
	}
	if _, err := a.staffOrganisationGrant(orgAdmin, &EntryRoleInput{ID: organisation.Body.ID, Role: "admins", PK: 99999}); statusOf(err) != http.StatusUnprocessableEntity {
		t.Errorf("a key nobody has: status %d, want 422", statusOf(err))
	}
	if _, err := a.staffOrganisationGrant(collectiveAdmin, &EntryRoleInput{ID: organisation.Body.ID, Role: "members", PK: kai.PK}); statusOf(err) != http.StatusForbidden {
		t.Errorf("somebody else adding a member: status %d, want 403", statusOf(err))
	}

	save := &SaveOrganisationInput{ID: organisation.Body.ID}
	save.Body.Name, save.Body.Kind = "ver.di Bezirk Köln", "union"
	if _, err := a.staffSaveOrganisation(orgAdmin, save); err != nil {
		t.Errorf("an organisation's administrator editing it: %v", err)
	}
	if _, err := a.staffSaveOrganisation(author, save); statusOf(err) != http.StatusForbidden {
		t.Errorf("somebody else editing it: status %d, want 403", statusOf(err))
	}
	if _, err := a.staffDeleteOrganisation(orgAdmin, &OrganisationIDInput{ID: organisation.Body.ID}); statusOf(err) != http.StatusForbidden {
		t.Errorf("an organisation's administrator deleting it: status %d, want 403 — that is the site's", statusOf(err))
	}

	// The pickers are for whoever gives roles.
	if _, err := a.staffListUsers(author, nil); statusOf(err) != http.StatusForbidden {
		t.Errorf("an author listing users: status %d, want 403", statusOf(err))
	}
	if users, err := a.staffListUsers(orgAdmin, nil); err != nil || len(users.Body.Users) == 0 {
		t.Errorf("an organisation's administrator listing users: %v", err)
	}
}

// TestWhoMayUseTheConsole: anybody with a role here, and nobody without.
func TestWhoMayUseTheConsole(t *testing.T) {
	a := newAPI(t)
	f := newFakeDirectory(t)
	provisioned(t, a, f)
	founded(t, a, "Bündnis Köln", "koeln")

	for name, tc := range map[string]struct {
		ctx  context.Context
		want bool
	}{
		"an administrator":          {admin(), true},
		"a user":                    {editor("U", "schmerz-users"), true},
		"an author of a collective": {editor("A", authorsOf("koeln")), true},
		"in no group of this site":  {editor("N", "somebody-elses-group"), false},
	} {
		me, err := a.staffMe(tc.ctx, nil)
		if err != nil || me.Body.Allowed != tc.want {
			t.Errorf("%s: allowed %v (%v), want %v", name, me.Body.Allowed, err, tc.want)
		}
	}
}

// TestNobodyIsCreatedWhoCannotBeHandedTheirAccount.
func TestNobodyIsCreatedWhoCannotBeHandedTheirAccount(t *testing.T) {
	a := newAPI(t)
	f := newFakeDirectory(t)
	provisioned(t, a, f)
	f.mu.Lock()
	f.recoveryBound = false
	f.mu.Unlock()
	maria := asStaff("sub-maria", "Maria", "schmerz-admins")
	before := f.called("POST /core/users/")

	invite := &InviteInput{}
	invite.Body.Username, invite.Body.Name, invite.Body.Admin = "kai", "Kai", true
	if _, err := a.staffInvitePerson(maria, invite); statusOf(err) != http.StatusConflict {
		t.Errorf("status %d, want 409", statusOf(err))
	}
	if f.called("POST /core/users/") != before {
		t.Error("an account was created that nobody could be handed")
	}
}

func TestTheAdminGroupKeepsItsOldName(t *testing.T) {
	if models.AdminGroupName(models.DefaultAppName) != "schmerz-admins" {
		t.Error("an installation configured by hand would lose its administrators on provisioning")
	}
}

// deadlineRecorder is a ResponseWriter that remembers having its write
// deadline cleared — httptest's recorder does not implement it.
type deadlineRecorder struct {
	*httptest.ResponseRecorder
	cleared bool
}

func (d *deadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	d.cleared = deadline.IsZero()
	return nil
}

// TestProvisioningOutlivesTheWriteDeadline: forty seconds against a remote
// Authentik, and the server allows thirty. Without clearing the deadline the
// answer carrying the first admin's only way in goes to a closed connection.
func TestProvisioningOutlivesTheWriteDeadline(t *testing.T) {
	a := newAPI(t)
	a.staffToken = "the-shared-secret"
	f := newFakeDirectory(t)
	handler := served(t, a)

	body, _ := json.Marshal(map[string]any{
		"instance_url": f.URL(), "token": "t", "console_url": "https://console.example",
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/console/auth/provision", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(staffauth.TokenHeader, "the-shared-secret")
	recorder := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if !recorder.cleared {
		t.Error("provisioning did not clear its write deadline")
	}
}

// TestRunningSetupAgainReconcilesWhatExists: the console moved, and somebody
// edited the application in Authentik. Running the wizard again — with
// --superuser — finds everything, changes only what is wrong, and says what.
func TestRunningSetupAgainReconcilesWhatExists(t *testing.T) {
	a := newAPI(t)
	f := newFakeDirectory(t)
	first := provisioned(t, a, f)
	if len(first.Reconciled) != 0 {
		t.Errorf("a first run reported reconciling %v", first.Reconciled)
	}
	f.mu.Lock()
	f.applications[0]["provider"] = 999
	f.mu.Unlock()
	providersBefore := f.called("POST /providers/oauth2/")

	in := &ProvisionInput{}
	in.Body.InstanceURL, in.Body.Token = f.URL(), "another-setup-token"
	in.Body.ConsoleURL = "https://new-console.example"
	in.Body.AdminUsername = "maria"
	in.Body.Reprovision = true
	out, err := a.consoleProvisionAuth(context.Background(), in)
	if err != nil {
		t.Fatalf("second provisioning: %v", err)
	}

	if f.called("POST /providers/oauth2/") != providersBefore || f.called("POST /core/applications/") != 1 {
		t.Error("an existing provider or application was created again")
	}
	uris, _ := f.provider()["redirect_uris"].([]any)
	if len(uris) != 1 || fmt.Sprint(uris[0]) != fmt.Sprint(map[string]any{"matching_mode": "strict", "url": "https://new-console.example/auth/callback"}) {
		t.Errorf("redirect_uris = %v, want the console's new address", f.provider()["redirect_uris"])
	}
	f.mu.Lock()
	pointed := f.applications[0]["provider"]
	f.mu.Unlock()
	if fmt.Sprint(pointed) != fmt.Sprint(f.provider()["pk"]) {
		t.Errorf("application points at %v, want this site's provider", pointed)
	}
	if len(out.Body.Reconciled) != 2 {
		t.Errorf("reconciled = %v, want the redirect and the application said", out.Body.Reconciled)
	}
	if accounts := f.accountsNamed("maria"); accounts != 1 {
		t.Errorf("maria has %d accounts: the first admin, who existed, was created again", accounts)
	}
	stored, _ := a.store.AuthSettings(context.Background())
	if stored.ConsoleURL != "https://new-console.example" {
		t.Errorf("stored console URL = %q", stored.ConsoleURL)
	}
}

// TestAnExistingAccountIsGivenItsRolesNotDuplicated.
func TestAnExistingAccountIsGivenItsRolesNotDuplicated(t *testing.T) {
	a := newAPI(t)
	f := newFakeDirectory(t)
	provisioned(t, a, f)
	f.addUser("kai", "their-own-group")
	maria := asStaff("sub-maria", "Maria", "schmerz-admins")

	invite := &InviteInput{}
	invite.Body.Username, invite.Body.Name = "kai", "A Different Name"
	out, err := a.staffInvitePerson(maria, invite)
	if err != nil {
		t.Fatalf("inviting somebody who already has an account: %v", err)
	}
	if !out.Body.Adopted || out.Body.Link != "" {
		t.Errorf("answer = %+v, want adopted and no link minted unasked", out.Body)
	}
	if accounts := f.accountsNamed("kai"); accounts != 1 {
		t.Errorf("kai has %d accounts, want the one they had", accounts)
	}
	groups := f.groupsOf("kai")
	if !slices.Contains(groups, "schmerz-users") || !slices.Contains(groups, "their-own-group") {
		t.Errorf("kai is in %v, want the users' group beside the groups they had", groups)
	}

	// A deactivated account is not reactivated by being given a role.
	f.addUser("gone")
	f.deactivate("gone")
	invite.Body.Username = "gone"
	if _, err := a.staffInvitePerson(maria, invite); statusOf(err) != http.StatusConflict {
		t.Errorf("a deactivated account: status %d, want 409", statusOf(err))
	}
}

// TestADeactivatedFirstAdminIsRefused.
func TestADeactivatedFirstAdminIsRefused(t *testing.T) {
	a := newAPI(t)
	f := newFakeDirectory(t)
	f.addUser("maria")
	f.deactivate("maria")

	in := &ProvisionInput{}
	in.Body.InstanceURL, in.Body.Token, in.Body.ConsoleURL = f.URL(), "t", "https://console.example"
	in.Body.AdminUsername = "maria"
	if _, err := a.consoleProvisionAuth(context.Background(), in); statusOf(err) != http.StatusBadGateway {
		t.Errorf("status %d, want the refusal", statusOf(err))
	}
	if done, _ := a.store.AuthProvisioned(context.Background()); done {
		t.Error("a site was provisioned with a deactivated administrator")
	}
}

// TestAnInstanceThatLacksAPermissionKeepsTheOperatorsToken: a codename this
// version does not know means no service account — and a backend that keeps
// working, rather than one switched to a token that cannot read a role.
func TestAnInstanceThatLacksAPermissionKeepsTheOperatorsToken(t *testing.T) {
	a := newAPI(t)
	f := newFakeDirectory(t)
	f.unknownPermissions = map[string]bool{"authentik_core.add_user_to_group": true}

	got := provisioned(t, a, f)
	if !got.Done || got.ServiceAccount != "" || !strings.Contains(got.ServiceAccountNote, "add_user_to_group") {
		t.Fatalf("result = %+v", got)
	}
	if f.called("POST /core/users/service_account/") != 0 || f.called("POST /rbac/roles/") != 0 {
		t.Error("something was created although a permission was missing")
	}
	stored, _ := a.store.AuthSettings(context.Background())
	if stored.APIToken != "key-of-schmerz-console" {
		t.Errorf("stored token %q, want the one minted for the operator", stored.APIToken)
	}
}

// TestAnEmailIsAWayToBeReached: the movement's organisers reach the people
// they work with by it. It is optional, kept on the account in the directory,
// shown where people are listed — and an account adopted keeps the address it
// had.
func TestAnEmailIsAWayToBeReached(t *testing.T) {
	a := newAPI(t)
	f := newFakeDirectory(t)
	provisioned(t, a, f)
	koeln := founded(t, a, "Bündnis Köln", "koeln")

	invite := &InviteInput{}
	invite.Body.Username, invite.Body.Name, invite.Body.Email = "kai", "Kai", " kai@example.org "
	if _, err := a.staffInvitePerson(admin(), invite); err != nil {
		t.Fatalf("staffInvitePerson: %v", err)
	}
	if created := f.body("POST /core/users/"); created["email"] != "kai@example.org" {
		t.Errorf("created with email %v", created["email"])
	}

	invite.Body.Username, invite.Body.Email = "noaddress", "nicht-erreichbar"
	if _, err := a.staffInvitePerson(admin(), invite); statusOf(err) != http.StatusUnprocessableEntity {
		t.Errorf("an address that is not one: status %d, want 422", statusOf(err))
	}
	invite.Body.Email = ""
	if _, err := a.staffInvitePerson(admin(), invite); err != nil {
		t.Errorf("nobody has to give an address: %v", err)
	}

	// Adopted: an address it lacks is added, one it has is kept.
	bare := f.addUser("bare")
	kept := f.addUser("kept")
	f.mu.Lock()
	kept.Email = "own@example.org"
	f.mu.Unlock()
	for _, username := range []string{"bare", "kept"} {
		invite.Body.Username, invite.Body.Email = username, "given@example.org"
		if _, err := a.staffInvitePerson(admin(), invite); err != nil {
			t.Fatalf("adopting %s: %v", username, err)
		}
	}
	f.mu.Lock()
	if bare.Email != "given@example.org" || kept.Email != "own@example.org" {
		t.Errorf("adopted: bare %q, kept %q — an address is added, never replaced", bare.Email, kept.Email)
	}
	f.mu.Unlock()

	// Changed, and cleared, by the site's administrators — for the console's
	// people only.
	kai := f.userNamed("kai")
	set := &SetEmailInput{PK: kai.PK}
	set.Body.Username, set.Body.Email = "kai", "kai@neu.example"
	if _, err := a.staffSetPersonEmail(admin(), set); err != nil {
		t.Fatalf("staffSetPersonEmail: %v", err)
	}
	if _, err := a.staffSetPersonEmail(editor("Lea", adminsOf("koeln")), set); statusOf(err) != http.StatusForbidden {
		t.Errorf("a collective's administrator changing an address: status %d, want 403", statusOf(err))
	}
	outsider := f.addUser("outsider")
	if _, err := a.staffSetPersonEmail(admin(), &SetEmailInput{PK: outsider.PK}); statusOf(err) != http.StatusNotFound {
		t.Errorf("somebody with no role here: status %d, want 404", statusOf(err))
	}

	people, err := a.staffListPeople(admin(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, person := range people.Body.People {
		found = found || (person.Username == "kai" && person.Email == "kai@neu.example")
	}
	if !found {
		t.Errorf("the people page does not carry kai's address: %+v", people.Body.People)
	}

	// Whoever runs a collective sees how to reach its people.
	grant := &EntryRoleInput{ID: koeln.ID, Role: "authors", PK: kai.PK, Username: "kai"}
	if _, err := a.staffCollectiveGrant(admin(), grant); err != nil {
		t.Fatal(err)
	}
	entry, err := a.staffCollectivePeople(editor("Lea", adminsOf("koeln")), &CollectiveIDInput{ID: koeln.ID})
	if err != nil || len(entry.Body.Others) != 1 || entry.Body.Others[0].Email != "kai@neu.example" {
		t.Errorf("the collective's authors = %+v, %v", entry, err)
	}

	set.Body.Email = ""
	if _, err := a.staffSetPersonEmail(admin(), set); err != nil {
		t.Errorf("clearing an address: %v", err)
	}
	f.mu.Lock()
	if kai.Email != "" {
		t.Errorf("cleared address is %q", kai.Email)
	}
	f.mu.Unlock()
}
