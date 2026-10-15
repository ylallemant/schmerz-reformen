package backend

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/ylallemant/schmerz-reformen/internal/ratelimit"
)

// onboarding is the join form's request.
func onboarding(email, name string) *OnboardInput {
	in := &OnboardInput{}
	in.Body.Email, in.Body.Name = email, name
	return in
}

// onboardable is a provisioned site whose wizard named one onboarding domain.
func onboardable(t *testing.T) (*API, *fakeDirectory) {
	t.Helper()
	a := newAPI(t)
	f := newFakeDirectory(t)
	in := &ProvisionInput{}
	in.Body.InstanceURL = f.URL()
	in.Body.Token = "a-pasted-setup-token"
	in.Body.ConsoleURL = "https://console.example/"
	in.Body.AdminUsername, in.Body.AdminName = "maria", "Maria"
	in.Body.OnboardingDomains = "@Sozialratschlag.de"
	if _, err := a.consoleProvisionAuth(context.Background(), in); err != nil {
		t.Fatalf("consoleProvisionAuth: %v", err)
	}
	return a, f
}

// TestSomebodyAtAnOnboardingDomainMakesTheirOwnAccountAndWaits: the account
// is made at once with the way in shown — and it is in no role, only in the
// waiting group, because the address it was made with proves nothing.
func TestSomebodyAtAnOnboardingDomainMakesTheirOwnAccountAndWaits(t *testing.T) {
	a, f := onboardable(t)

	settings, err := a.consoleGetAuth(context.Background(), nil)
	if err != nil || !slices.Equal(settings.Body.OnboardingDomains, []string{"sozialratschlag.de"}) {
		t.Fatalf("the wizard's domains were not kept: %+v, %v", settings, err)
	}
	if !slices.Contains(f.groupNames(), "schmerz-pending") {
		t.Error("provisioning did not make the waiting group")
	}

	out, err := a.consoleOnboard(context.Background(), onboarding(" Anna@Sozialratschlag.DE ", "Anna"))
	if err != nil {
		t.Fatalf("consoleOnboard: %v", err)
	}
	if out.Body.Username != "anna@sozialratschlag.de" || out.Body.Link == "" {
		t.Errorf("answer = %+v, want the address as username and a way in", out.Body)
	}
	if groups := f.groupsOf("anna@sozialratschlag.de"); !slices.Equal(groups, []string{"schmerz-pending"}) {
		t.Errorf("anna is in %v, want only the waiting group", groups)
	}
	if created := f.body("POST /core/users/"); created["email"] != "anna@sozialratschlag.de" || created["name"] != "Anna" {
		t.Errorf("created %v", created)
	}

	// At the door: no role, and told why.
	me, err := a.staffMe(asStaff("sub-anna", "Anna", "schmerz-pending"), nil)
	if err != nil || me.Body.Allowed || !me.Body.Pending {
		t.Errorf("anna's profile: allowed %v pending %v, %v", me.Body.Allowed, me.Body.Pending, err)
	}
	me, err = a.staffMe(admin(), nil)
	if err != nil || me.Body.Waiting != 1 {
		t.Errorf("an administrator is told %d wait, want 1 (%v)", me.Body.Waiting, err)
	}
	people, err := a.staffListPeople(admin(), nil)
	if err != nil || len(people.Body.Pending) != 1 || people.Body.Pending[0].Email != "anna@sozialratschlag.de" ||
		!slices.Equal(people.Body.OnboardingDomains, []string{"sozialratschlag.de"}) {
		t.Errorf("people page: pending %+v, domains %v, %v", people.Body.Pending, people.Body.OnboardingDomains, err)
	}
	for _, person := range people.Body.People {
		if person.Username == "anna@sozialratschlag.de" {
			t.Error("somebody waiting is listed among the console's people")
		}
	}
}

// TestOnlyTheChosenDomainsMayJoin, and only with an address and a name.
func TestOnlyTheChosenDomainsMayJoin(t *testing.T) {
	a, f := onboardable(t)
	made := f.called("POST /core/users/")
	for _, tc := range []struct {
		email, name string
		want        int
	}{
		{"anna@verdi.de", "Anna", http.StatusForbidden},
		{"anna@mail.sozialratschlag.de", "Anna", http.StatusForbidden},
		{"anna@sozialratschlag.de.example", "Anna", http.StatusForbidden},
		{"keine-adresse", "Anna", http.StatusUnprocessableEntity},
		{"", "Anna", http.StatusUnprocessableEntity},
		{"anna@sozialratschlag.de", "", http.StatusUnprocessableEntity},
	} {
		if _, err := a.consoleOnboard(context.Background(), onboarding(tc.email, tc.name)); statusOf(err) != tc.want {
			t.Errorf("%q / %q: status %d, want %d", tc.email, tc.name, statusOf(err), tc.want)
		}
	}
	if f.called("POST /core/users/") != made {
		t.Error("an account was made for a refused request")
	}

	// Switched off by emptying the list.
	if _, err := a.staffSetOnboardingDomains(admin(), domainsInput("")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.consoleOnboard(context.Background(), onboarding("anna@sozialratschlag.de", "Anna")); statusOf(err) != http.StatusForbidden {
		t.Errorf("with no domains: status %d, want 403", statusOf(err))
	}
}

// TestNoWayInIsEverMadeForAnAccountThatExists: whoever typed its address
// would be handed the means to replace its credentials.
func TestNoWayInIsEverMadeForAnAccountThatExists(t *testing.T) {
	a, f := onboardable(t)

	if _, err := a.consoleOnboard(context.Background(), onboarding("anna@sozialratschlag.de", "Anna")); err != nil {
		t.Fatal(err)
	}
	// Again: the account is hers, waiting or not.
	if _, err := a.consoleOnboard(context.Background(), onboarding("anna@sozialratschlag.de", "Anna")); statusOf(err) != http.StatusConflict {
		t.Errorf("the same address twice: status %d, want 409", statusOf(err))
	}

	// An account under another username that carries the address.
	kai := f.addUser("kai", "schmerz-users")
	f.mu.Lock()
	kai.Email = "kai@sozialratschlag.de"
	f.mu.Unlock()
	made := f.called("POST /core/users/")
	if _, err := a.consoleOnboard(context.Background(), onboarding("KAI@sozialratschlag.de", "Kai")); statusOf(err) != http.StatusConflict {
		t.Errorf("an address an account already carries: status %d, want 409", statusOf(err))
	}
	if f.called("POST /core/users/") != made {
		t.Error("a way in or an account was made for an address that already has one")
	}
}

// TestAnAdministratorLetsInOrTurnsAway — only people who are waiting.
func TestAnAdministratorLetsInOrTurnsAway(t *testing.T) {
	a, f := onboardable(t)
	for _, email := range []string{"anna@sozialratschlag.de", "fake@sozialratschlag.de"} {
		if _, err := a.consoleOnboard(context.Background(), onboarding(email, "Jemand")); err != nil {
			t.Fatal(err)
		}
	}
	anna, fake := f.userNamed("anna@sozialratschlag.de"), f.userNamed("fake@sozialratschlag.de")

	if _, err := a.staffApprovePerson(editor("Lea", adminsOf("koeln")), &ApprovalInput{PK: anna.PK}); statusOf(err) != http.StatusForbidden {
		t.Errorf("a non-administrator approving: status %d, want 403", statusOf(err))
	}
	if _, err := a.staffApprovePerson(admin(), &ApprovalInput{PK: anna.PK, Username: anna.Username}); err != nil {
		t.Fatalf("staffApprovePerson: %v", err)
	}
	if groups := f.groupsOf(anna.Username); !slices.Equal(groups, []string{"schmerz-users"}) {
		t.Errorf("approved anna is in %v, want the users' group only", groups)
	}

	if _, err := a.staffRefusePerson(admin(), &ApprovalInput{PK: fake.PK, Username: fake.Username}); err != nil {
		t.Fatalf("staffRefusePerson: %v", err)
	}
	f.mu.Lock()
	if fake.Active || len(fake.Groups) != 0 {
		t.Errorf("refused account: active %v, groups %v — want it deactivated and in nothing", fake.Active, fake.Groups)
	}
	f.mu.Unlock()

	// Somebody not waiting is neither let in nor deactivated through here:
	// that would be a way to act on anybody in the directory.
	outsider := f.addUser("outsider")
	for name, call := range map[string]func() error{
		"approve": func() error { _, err := a.staffApprovePerson(admin(), &ApprovalInput{PK: outsider.PK}); return err },
		"refuse":  func() error { _, err := a.staffRefusePerson(admin(), &ApprovalInput{PK: outsider.PK}); return err },
		"again":   func() error { _, err := a.staffApprovePerson(admin(), &ApprovalInput{PK: anna.PK}); return err },
	} {
		if err := call(); statusOf(err) != http.StatusNotFound {
			t.Errorf("%s somebody not waiting: status %d, want 404", name, statusOf(err))
		}
	}
	f.mu.Lock()
	if !outsider.Active {
		t.Error("an account that was not waiting was deactivated")
	}
	f.mu.Unlock()
}

// TestTheFormCannotFloodTheDirectory: made fast, refused; too many waiting,
// refused until somebody has looked.
func TestTheFormCannotFloodTheDirectory(t *testing.T) {
	a, f := onboardable(t)
	a.onboardings = ratelimit.New(1, time.Hour)
	if _, err := a.consoleOnboard(context.Background(), onboarding("eins@sozialratschlag.de", "Eins")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.consoleOnboard(context.Background(), onboarding("zwei@sozialratschlag.de", "Zwei")); statusOf(err) != http.StatusTooManyRequests {
		t.Errorf("a second account at once: status %d, want 429", statusOf(err))
	}

	a.onboardings = nil
	for i := range maxWaiting {
		f.addUser(fmt.Sprintf("waiting-%d", i), "schmerz-pending")
	}
	if _, err := a.consoleOnboard(context.Background(), onboarding("drei@sozialratschlag.de", "Drei")); statusOf(err) != http.StatusServiceUnavailable {
		t.Errorf("with %d waiting: status %d, want 503", maxWaiting, statusOf(err))
	}
}

// TestDomainsAreTheSiteAdministrators.
func TestDomainsAreTheSiteAdministrators(t *testing.T) {
	a, _ := onboardable(t)
	if _, err := a.staffSetOnboardingDomains(editor("Lea", adminsOf("koeln")), domainsInput("verdi.de")); statusOf(err) != http.StatusForbidden {
		t.Errorf("a non-administrator: status %d, want 403", statusOf(err))
	}
	if _, err := a.staffSetOnboardingDomains(admin(), domainsInput("verdi.de, <b>")); statusOf(err) != http.StatusUnprocessableEntity {
		t.Errorf("something that is not a domain: status %d, want 422", statusOf(err))
	}
	out, err := a.staffSetOnboardingDomains(admin(), domainsInput("verdi.de\nsozialratschlag.de"))
	if err != nil || !slices.Equal(out.Body.Domains, []string{"verdi.de", "sozialratschlag.de"}) {
		t.Errorf("set = %+v, %v", out, err)
	}
	if _, err := a.consoleOnboard(context.Background(), onboarding("anna@verdi.de", "Anna")); err != nil {
		t.Errorf("a domain added from the console does not admit: %v", err)
	}
}

func domainsInput(domains string) *OnboardingDomainsInput {
	in := &OnboardingDomainsInput{}
	in.Body.Domains = domains
	return in
}

// TestAnInstallationOlderThanTheWaitingGroupStillListsItsPeople — met in
// production: set up before setup made schmerz-pending, so the group did not
// exist, and Authentik refuses to list the members of a group it does not
// have. The people page failed whole.
func TestAnInstallationOlderThanTheWaitingGroupStillListsItsPeople(t *testing.T) {
	a := newAPI(t)
	f := newFakeDirectory(t)
	provisioned(t, a, f)
	f.mu.Lock()
	for pk, group := range f.groups {
		if group.Name == "schmerz-pending" {
			delete(f.groups, pk)
		}
	}
	f.mu.Unlock()

	people, err := a.staffListPeople(admin(), nil)
	if err != nil {
		t.Fatalf("the people page with no waiting group: %v", err)
	}
	if len(people.Body.Pending) != 0 || len(people.Body.People) == 0 {
		t.Errorf("pending %+v, people %d", people.Body.Pending, len(people.Body.People))
	}
	if me, err := a.staffMe(admin(), nil); err != nil || me.Body.Waiting != 0 {
		t.Errorf("waiting %d, %v", me.Body.Waiting, err)
	}
}
