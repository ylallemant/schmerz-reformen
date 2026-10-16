package backend

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/ylallemant/schmerz-reformen/internal/config"
)

// TestASiteMovedUnderThePlatformIsBroughtInLine — production's case: set up
// by the wizard against the shared address, then given the platform's
// address, client and service account. What setup stored is stale, the page
// says each difference, and aligning makes it what the deployment gives — and
// nothing the request could choose.
func TestASiteMovedUnderThePlatformIsBroughtInLine(t *testing.T) {
	a := newAPI(t)
	f := newFakeDirectory(t)
	provisioned(t, a, f) // the wizard's own client, token and account
	ctx := context.Background()

	// Moved: the same directory, reached on the platform's address with the
	// platform's token.
	platformAddress := strings.Replace(f.URL(), "127.0.0.1", "localhost", 1)
	a.directory.access = &config.DirectoryAccess{URL: platformAddress, Token: "the-platform-token"}
	a.connectDirectory(ctx)

	status, err := a.staffSignInStatus(admin(), &SignInQuery{ConsolePlatform: true})
	if err != nil {
		t.Fatal(err)
	}
	var fields []string
	for _, difference := range status.Body.Differences {
		fields = append(fields, difference.Field)
	}
	for _, want := range []string{"instance_url", "directory_by_platform", "stored_token", "sign_in_by_platform", "stored_client"} {
		if !slices.Contains(fields, want) {
			t.Errorf("differences %v lack %s", fields, want)
		}
	}
	if status.Body.DirectoryAddress != platformAddress || !status.Body.DirectoryFromEnv {
		t.Errorf("effective directory %q (env %v), want the platform's address", status.Body.DirectoryAddress, status.Body.DirectoryFromEnv)
	}
	if len(status.Body.Groups) != 3 || !status.Body.Groups[0].Exists {
		t.Errorf("groups %+v", status.Body.Groups)
	}

	// What setup stored, and the directory's account, are an administrator's
	// to read.
	if _, err := a.staffSignInStatus(editor("Lea", adminsOf("koeln")), &SignInQuery{}); statusOf(err) != http.StatusForbidden {
		t.Errorf("a non-administrator reading the page: status %d, want 403", statusOf(err))
	}
	if _, err := a.staffAlignSignIn(editor("Lea", adminsOf("koeln")), &AlignSignInInput{}); statusOf(err) != http.StatusForbidden {
		t.Errorf("a non-administrator aligning: status %d, want 403", statusOf(err))
	}

	align := &AlignSignInInput{}
	align.Body.ConsolePlatform = true
	aligned, err := a.staffAlignSignIn(admin(), align)
	if err != nil {
		t.Fatalf("staffAlignSignIn: %v", err)
	}
	if len(aligned.Body.Differences) != 0 {
		t.Errorf("after aligning, still %+v", aligned.Body.Differences)
	}
	settings, err := a.store.AuthSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.InstanceURL != platformAddress || settings.APIToken != "" || settings.ClientID != "" ||
		settings.ClientSecret != "" || !settings.SignInByPlatform || !settings.DirectoryByPlatform {
		t.Errorf("stored after aligning: %+v", settings)
	}
	if settings.AppName != "schmerz" || settings.ConsoleURL == "" {
		t.Errorf("aligning changed what it had no business with: %+v", settings)
	}
}

// TestNothingToAlignBeforeSetup — the page says so, and aligning refuses.
func TestNothingToAlignBeforeSetup(t *testing.T) {
	a := newAPI(t)
	status, err := a.staffSignInStatus(admin(), &SignInQuery{})
	if err != nil || status.Body.Provisioned || len(status.Body.Differences) != 0 || status.Body.DirectoryProblem == "" {
		t.Errorf("before setup: %+v, %v", status.Body, err)
	}
	if _, err := a.staffAlignSignIn(admin(), &AlignSignInInput{}); statusOf(err) != http.StatusConflict {
		t.Errorf("aligning before setup: status %d, want 409", statusOf(err))
	}
}
