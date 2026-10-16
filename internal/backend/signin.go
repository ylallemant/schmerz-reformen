package backend

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/authentik"
	"github.com/ylallemant/schmerz-reformen/internal/models"
)

// The console's sign-in page: what setup stored, what this installation
// actually uses, and whether the directory is ready to hand people a way in.
//
// # It aligns, it never repoints
//
// The stored settings say which directory this site answers to. Pointing them
// at another one is the setup wizard's, behind a restart with --superuser and
// a person on the maintenance port, because whoever can do it can hand the
// site to whoever runs that directory. So this page takes no address, no
// client and no secret from the request: it can only bring what was stored in
// line with what the deployment already gives — the platform's address, its
// sign-in client, its service account — which is what changes when a site
// moves under the platform, and what was left stale when it did.

func (a *API) registerSignInRoutes(api huma.API) {
	huma.Register(api, adminOnly(huma.Operation{
		OperationID: "staff-sign-in-status",
		Method:      http.MethodGet,
		Path:        "/v1/staff/sign-in",
		Summary:     "How this site signs people in and reaches its directory",
		Description: "What setup stored beside what the deployment gives, each difference between " +
			"them, and whether the directory can hand people a way in. Administrators only.",
		Tags: []string{"Setup"},
	}), a.staffSignInStatus)

	huma.Register(api, adminOnly(huma.Operation{
		OperationID: "staff-align-sign-in",
		Method:      http.MethodPut,
		Path:        "/v1/staff/sign-in",
		Summary:     "Bring what setup stored in line with what the deployment gives",
		Description: "Takes no address and no secret: only what the backend's environment and the " +
			"console's already say. Pointing the site at another directory is the setup wizard's.",
		Tags: []string{"Setup"},
	}), a.staffAlignSignIn)
}

// SignInQuery is what the console says of itself: whether it signs people in
// with the deployment platform's client. Only the console knows.
type SignInQuery struct {
	ConsolePlatform bool `query:"console_platform" doc:"the console signs in with the deployment platform's client"`
}

// SignInDifference is one stored value the deployment says otherwise.
type SignInDifference struct {
	// Field is a stable name the console words: instance_url,
	// directory_by_platform, stored_token, sign_in_by_platform, stored_client.
	Field     string `json:"field"`
	Stored    string `json:"stored"`
	Effective string `json:"effective"`
}

// SignInGroup is one of the site's static groups, and whether it exists.
type SignInGroup struct {
	Name   string `json:"name"`
	Exists bool   `json:"exists"`
}

// SignInStatusOutput is the page.
type SignInStatusOutput struct {
	Body SignInStatus
}

// SignInStatus is what the page shows.
type SignInStatus struct {
	Provisioned bool `json:"provisioned"`

	// What setup stored.
	AppName             string     `json:"app_name,omitempty"`
	StoredInstance      string     `json:"stored_instance,omitempty"`
	ConsoleURL          string     `json:"console_url,omitempty"`
	StoredClientID      string     `json:"stored_client_id,omitempty"`
	SignInByPlatform    bool       `json:"sign_in_by_platform"`
	DirectoryByPlatform bool       `json:"directory_by_platform"`
	ProvisionedBy       string     `json:"provisioned_by,omitempty"`
	ProvisionedAt       *time.Time `json:"provisioned_at,omitempty"`

	// What is used: the directory's address and whose token reaches it.
	DirectoryAddress    string `json:"directory_address,omitempty"`
	DirectoryAccount    string `json:"directory_account,omitempty"`
	DirectoryFromEnv    bool   `json:"directory_from_environment"`
	DirectoryProblem    string `json:"directory_problem,omitempty"`
	BrandDomain         string `json:"brand_domain,omitempty"`
	BrandIsDefault      bool   `json:"brand_is_default"`
	BrandHasRecovery    bool   `json:"brand_has_recovery"`
	RecoveryFlow        string `json:"recovery_flow,omitempty"`
	RecoveryFlowIsOurs  bool   `json:"recovery_flow_is_ours"`
	RecoveryFlowMissing bool   `json:"recovery_flow_missing"`

	Groups []SignInGroup `json:"groups"`

	// Differences are what Align would change; empty, nothing to do.
	Differences []SignInDifference `json:"differences"`
}

func (a *API) staffSignInStatus(ctx context.Context, in *SignInQuery) (*SignInStatusOutput, error) {
	if _, err := mustAdmin(ctx); err != nil {
		return nil, err
	}
	settings, err := a.store.AuthSettings(ctx)
	if err != nil {
		log.Error().Err(err).Msg("cannot read the sign-in settings")
		return nil, huma.Error500InternalServerError("cannot read the sign-in settings")
	}

	out := &SignInStatusOutput{}
	body := &out.Body
	body.Provisioned = settings.Provisioned
	body.AppName, body.StoredInstance, body.ConsoleURL = settings.AppName, settings.InstanceURL, settings.ConsoleURL
	body.StoredClientID = settings.ClientID
	body.SignInByPlatform, body.DirectoryByPlatform = settings.SignInByPlatform, settings.DirectoryByPlatform
	body.ProvisionedBy, body.ProvisionedAt = settings.ProvisionedBy, settings.ProvisionedAt
	body.DirectoryFromEnv = a.directory.access != nil
	body.Groups = []SignInGroup{}
	body.Differences = differences(settings, a.directory.access != nil, a.effectiveInstance(settings), in.ConsolePlatform)

	client, _ := a.directory.connected()
	if client == nil {
		body.DirectoryProblem = "the backend is connected to no directory"
		return out, nil
	}
	body.DirectoryAddress = client.InstanceURL()
	a.checkDirectory(ctx, client, settings.AppName, body)
	return out, nil
}

// checkDirectory asks the directory what the page shows. Each answer is a
// line on the page, so one that fails is said there rather than failing it.
func (a *API) checkDirectory(ctx context.Context, client *authentik.Client, appName string, body *SignInStatus) {
	me, err := client.Me(ctx)
	if err != nil {
		body.DirectoryProblem = directoryRefusal(err, "the directory refused the backend's token").Error()
		return
	}
	body.DirectoryAccount = me.Username

	brand, err := client.Brand(ctx)
	if err != nil {
		body.DirectoryProblem = "cannot read the brand: " + err.Error()
		return
	}
	body.BrandDomain, body.BrandIsDefault = brand.Domain, brand.Default
	body.BrandHasRecovery = brand.RecoveryFlow != ""

	if appName != "" {
		ours, err := client.Flow(ctx, appName+"-recovery")
		switch {
		case errors.Is(err, authentik.ErrNotFound):
			body.RecoveryFlowMissing = true
		case err != nil:
			log.Warn().Err(err).Msg("cannot read this site's recovery flow")
		default:
			body.RecoveryFlow = ours.Slug
			body.RecoveryFlowIsOurs = brand.RecoveryFlow != "" && brand.RecoveryFlow == ours.PK
		}
		for _, name := range []string{models.AdminGroupName(appName), models.UsersGroupName(appName), models.PendingGroupName(appName)} {
			_, err := client.GroupByName(ctx, name)
			body.Groups = append(body.Groups, SignInGroup{Name: name, Exists: err == nil})
		}
	}
}

// effectiveInstance is the directory's address the backend uses: the
// environment's when the platform gives it, what setup stored otherwise.
func (a *API) effectiveInstance(settings models.AuthSettings) string {
	if client, _ := a.directory.connected(); client != nil && a.directory.access != nil {
		return client.InstanceURL()
	}
	return settings.InstanceURL
}

// differences are the stored values the deployment says otherwise — which is
// everything Align changes, and nothing else.
func differences(settings models.AuthSettings, directoryFromEnv bool, effective string, consolePlatform bool) []SignInDifference {
	out := []SignInDifference{}
	if !settings.Provisioned {
		return out
	}
	yes := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	if directoryFromEnv && settings.InstanceURL != effective {
		out = append(out, SignInDifference{Field: "instance_url", Stored: settings.InstanceURL, Effective: effective})
	}
	if settings.DirectoryByPlatform != directoryFromEnv {
		out = append(out, SignInDifference{Field: "directory_by_platform",
			Stored: yes(settings.DirectoryByPlatform), Effective: yes(directoryFromEnv)})
	}
	if directoryFromEnv && settings.APIToken != "" {
		out = append(out, SignInDifference{Field: "stored_token", Stored: "yes", Effective: "no"})
	}
	if settings.SignInByPlatform != consolePlatform {
		out = append(out, SignInDifference{Field: "sign_in_by_platform",
			Stored: yes(settings.SignInByPlatform), Effective: yes(consolePlatform)})
	}
	if consolePlatform && settings.ClientID != "" {
		out = append(out, SignInDifference{Field: "stored_client", Stored: settings.ClientID, Effective: ""})
	}
	return out
}

// AlignSignInInput is what the console says of itself; nothing else is read.
type AlignSignInInput struct {
	Body struct {
		ConsolePlatform bool `json:"console_platform" doc:"the console signs in with the deployment platform's client"`
	}
}

func (a *API) staffAlignSignIn(ctx context.Context, in *AlignSignInInput) (*SignInStatusOutput, error) {
	who, err := mustAdmin(ctx)
	if err != nil {
		return nil, err
	}
	settings, err := a.store.AuthSettings(ctx)
	if err != nil {
		log.Error().Err(err).Msg("cannot read the sign-in settings")
		return nil, huma.Error500InternalServerError("cannot read the sign-in settings")
	}
	if !settings.Provisioned {
		return nil, huma.Error409Conflict("nothing is set up yet: run the console's setup first")
	}
	fromEnv := a.directory.access != nil
	changes := differences(settings, fromEnv, a.effectiveInstance(settings), in.Body.ConsolePlatform)
	if len(changes) == 0 {
		return a.staffSignInStatus(ctx, &SignInQuery{ConsolePlatform: in.Body.ConsolePlatform})
	}

	if fromEnv {
		// The platform's account is what reaches the directory: its address
		// is the one to keep, and a stored token is a token nobody uses —
		// one the platform has, by now, set to something else.
		settings.InstanceURL = a.effectiveInstance(settings)
		settings.APIToken = ""
	}
	settings.DirectoryByPlatform = fromEnv
	settings.SignInByPlatform = in.Body.ConsolePlatform
	if in.Body.ConsolePlatform {
		// The console signs in with the platform's client; the one setup
		// made is used by nothing.
		settings.ClientID, settings.ClientSecret = "", ""
	}

	if err := a.store.SaveAuthSettings(ctx, settings, true); err != nil {
		log.Error().Err(err).Msg("cannot store the aligned sign-in settings")
		return nil, huma.Error500InternalServerError("cannot store the sign-in settings")
	}
	a.useDirectory(settings)

	fields := make([]string, 0, len(changes))
	for _, change := range changes {
		fields = append(fields, change.Field)
	}
	log.Warn().Str("by", who.Username).Strs("aligned", fields).
		Msg("the stored sign-in settings were brought in line with the deployment")
	return a.staffSignInStatus(ctx, &SignInQuery{ConsolePlatform: in.Body.ConsolePlatform})
}
