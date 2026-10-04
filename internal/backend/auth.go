package backend

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/authentik"
	"github.com/ylallemant/schmerz-reformen/internal/models"
	"github.com/ylallemant/schmerz-reformen/internal/store"
)

// The console's sign-in configuration, as its setup wizard provisions it.
//
// Three routes, all the console's own and all made before anybody is signed
// in, so they need the console's secret and no editor: read what is
// configured, read the one secret the console is given, and provision.
//
// There is deliberately no route that *clears* the configuration. Reopening
// the setup door is a restart of the console with --superuser — a thing
// somebody does on purpose at a terminal, not a request anybody can make.
func (a *API) registerAuthRoutes(api huma.API) {
	huma.Register(api, consoleOnly(huma.Operation{
		OperationID: "console-get-auth",
		Method:      http.MethodGet,
		Path:        "/v1/console/auth",
		Summary:     "How the console signs editors in",
		Description: "Carries no secret. Whether the setup wizard has run, and against which " +
			"Authentik application.",
		Tags: []string{"Setup"},
	}), a.consoleGetAuth)

	huma.Register(api, consoleOnly(huma.Operation{
		OperationID: "console-get-auth-credentials",
		Method:      http.MethodGet,
		Path:        "/v1/console/auth/credentials",
		Summary:     "The OAuth client secret the console exchanges codes with",
		Description: "The one secret the console holds: exchanging an authorisation code is its " +
			"job, because it is the one with the browser. The directory token never leaves " +
			"the backend.",
		Tags: []string{"Setup"},
	}), a.consoleGetAuthCredentials)

	huma.Register(api, consoleOnly(huma.Operation{
		OperationID: "console-provision-auth",
		Method:      http.MethodPost,
		Path:        "/v1/console/auth/provision",
		Summary:     "Provision the console's identity provider",
		Description: "Creates this site's OAuth2 provider, application and administrators' group " +
			"in an Authentik instance, a recovery flow if it has none, and a token of the " +
			"backend's own; then stores the lot. Refused once an installation is provisioned " +
			"unless `reprovision` says it means to replace it. Takes tens of seconds against a " +
			"remote instance.",
		Tags: []string{"Setup"},
		// Twenty-odd round trips to somebody else's Authentik — forty seconds
		// measured — outlive the server's thirty-second write deadline, which
		// runs from when the request headers were read. Without this the
		// handler finishes, every object exists, and the answer carrying the
		// first admin's only way in is written to a closed connection.
		Middlewares: huma.Middlewares{func(ctx huma.Context, next func(huma.Context)) {
			if _, w := humago.Unwrap(ctx); w != nil {
				_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
			}
			next(ctx)
		}},
	}), a.consoleProvisionAuth)
}

// AuthItem is the configuration as the console may see it.
type AuthItem struct {
	Provisioned bool   `json:"provisioned"`
	AppName     string `json:"app_name,omitempty"`
	InstanceURL string `json:"instance_url,omitempty"`
	ConsoleURL  string `json:"console_url,omitempty"`
	ClientID    string `json:"client_id,omitempty"`

	// Issuer is the application's OIDC issuer, which discovery hangs off.
	Issuer string `json:"issuer,omitempty"`

	// AdminGroupName is who administers: the console draws its
	// administrator-only menus from it.
	AdminGroupName string `json:"admin_group_name,omitempty"`

	ProvisionedBy string     `json:"provisioned_by,omitempty"`
	ProvisionedAt *time.Time `json:"provisioned_at,omitempty"`
}

// AuthOutput is the configuration.
type AuthOutput struct {
	Body AuthItem
}

func (a *API) consoleGetAuth(ctx context.Context, _ *struct{}) (*AuthOutput, error) {
	provisioned, err := a.store.AuthProvisioned(ctx)
	if err != nil {
		log.Error().Err(err).Msg("cannot read whether an identity provider is configured")
		return nil, huma.Error500InternalServerError("cannot read the sign-in configuration")
	}
	if !provisioned {
		return &AuthOutput{Body: AuthItem{AdminGroupName: a.currentAdminGroup()}}, nil
	}

	settings, err := a.store.AuthSettings(ctx)
	if err != nil {
		// Configured, and unreadable: a settings key that is not the one the
		// row was sealed with. Said as configured, so the console keeps its
		// setup door shut; the backend's own log says what is wrong.
		log.Error().Err(err).Msg("the identity provider's settings cannot be read")
		return &AuthOutput{Body: AuthItem{Provisioned: true}}, nil
	}
	return &AuthOutput{Body: AuthItem{
		Provisioned:    true,
		AppName:        settings.AppName,
		InstanceURL:    settings.InstanceURL,
		ConsoleURL:     settings.ConsoleURL,
		ClientID:       settings.ClientID,
		Issuer:         settings.Issuer(),
		AdminGroupName: settings.AdminGroupName(),
		ProvisionedBy:  settings.ProvisionedBy,
		ProvisionedAt:  settings.ProvisionedAt,
	}}, nil
}

// AuthCredentialsOutput is the client secret.
type AuthCredentialsOutput struct {
	Body struct {
		ClientSecret string `json:"client_secret"`
	}
}

func (a *API) consoleGetAuthCredentials(ctx context.Context, _ *struct{}) (*AuthCredentialsOutput, error) {
	settings, err := a.store.AuthSettings(ctx)
	if err != nil {
		log.Error().Err(err).Msg("cannot read the identity provider's credentials")
		return nil, huma.Error500InternalServerError("cannot read the sign-in credentials")
	}
	if !settings.Provisioned {
		return nil, huma.Error404NotFound("no identity provider is configured")
	}
	out := &AuthCredentialsOutput{}
	out.Body.ClientSecret = settings.ClientSecret
	return out, nil
}

// ProvisionInput is what the setup wizard was given.
type ProvisionInput struct {
	Body struct {
		InstanceURL string `json:"instance_url" doc:"the Authentik instance"`
		Token       string `json:"token" doc:"an Authentik API token, used for this request and not kept"`
		AppName     string `json:"app_name,omitempty" doc:"the application slug and group prefix; empty is schmerz"`
		ConsoleURL  string `json:"console_url" doc:"where editors reach the console; the redirect is registered under it"`

		// AdminUsername names the first administrator. Empty is whoever owns
		// the token — on a fresh Authentik the bootstrap akadmin.
		AdminUsername string `json:"admin_username,omitempty"`
		AdminName     string `json:"admin_name,omitempty"`

		// Reprovision replaces a configuration that already exists. The
		// console sends it only when it was started with --superuser.
		Reprovision bool `json:"reprovision,omitempty"`
	}
}

// ProvisionItem is what the wizard shows.
type ProvisionItem struct {
	Done          bool     `json:"done"`
	Missing       []string `json:"missing,omitempty"`
	AdminUsername string   `json:"admin_username,omitempty"`
	TokenOwner    string   `json:"token_owner,omitempty"`
	InstanceURL   string   `json:"instance_url,omitempty"`
	AppName       string   `json:"app_name,omitempty"`
	RecoveryReady bool     `json:"recovery_ready"`

	// OwnToken names the token the backend minted for itself, so an operator
	// can find and revoke it.
	OwnToken string `json:"own_token,omitempty"`

	// AdminLink is the first admin's way in, returned once and kept nowhere.
	AdminLink string `json:"admin_link,omitempty"`

	// Reconciled is what already existed and was brought back in line.
	Reconciled []string `json:"reconciled,omitempty"`

	// ServiceAccount is the account the backend works as from now on, or
	// ServiceAccountNote why it kept the operator's token.
	ServiceAccount     string `json:"service_account,omitempty"`
	ServiceAccountNote string `json:"service_account_note,omitempty"`
}

// ProvisionOutput is the outcome.
type ProvisionOutput struct {
	Body ProvisionItem
}

func (a *API) consoleProvisionAuth(ctx context.Context, in *ProvisionInput) (*ProvisionOutput, error) {
	appName := strings.TrimSpace(in.Body.AppName)
	if appName == "" {
		appName = models.DefaultAppName
	}
	if !models.ValidAppName(appName) {
		return nil, huma.Error422UnprocessableEntity(
			"the name must be lowercase letters and digits, with - or _ inside")
	}
	consoleURL := strings.TrimRight(strings.TrimSpace(in.Body.ConsoleURL), "/")
	if parsed, err := url.Parse(consoleURL); err != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, huma.Error422UnprocessableEntity("the console's address must be an http or https URL")
	}

	if !in.Body.Reprovision {
		// Asked before anything is created in the directory: refusing the
		// store write afterwards would leave a provisioned directory and a
		// site that does not use it.
		if done, err := a.store.AuthProvisioned(ctx); err == nil && done {
			return nil, huma.Error409Conflict(
				"this console already has an identity provider; restart it with --superuser to replace it")
		}
	}

	client, err := authentik.New(in.Body.InstanceURL, in.Body.Token)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}

	result, err := provision(ctx, client, consoleURL, appName, firstAdmin{
		Username: in.Body.AdminUsername, Name: in.Body.AdminName,
	})
	out := &ProvisionOutput{Body: ProvisionItem{
		Done: result.Done, Missing: result.Missing, AdminUsername: result.AdminUsername,
		TokenOwner: result.TokenOwner, InstanceURL: result.InstanceURL, AppName: result.AppName,
		RecoveryReady: result.RecoveryReady, OwnToken: result.ConsoleToken.Identifier,
		AdminLink: result.AdminLink, Reconciled: result.Reconciled,
		ServiceAccount: result.ServiceAccount, ServiceAccountNote: result.ServiceAccountNote,
	}}
	if err != nil {
		// Authentik's own words where there are any: "slug: this field must be
		// unique" tells an operator what to do, a status code does not.
		log.Error().Err(err).Str("instance", in.Body.InstanceURL).Msg("provisioning failed")
		return nil, huma.Error502BadGateway(err.Error())
	}
	if !result.Done {
		return out, nil
	}

	err = a.store.SaveAuthSettings(ctx, result.Settings, in.Body.Reprovision)
	if errors.Is(err, store.ErrAlreadyProvisioned) {
		return nil, huma.Error409Conflict(
			"this console already has an identity provider; restart it with --superuser to replace it")
	}
	if err != nil {
		log.Error().Err(err).Msg("cannot store what was provisioned")
		return nil, huma.Error500InternalServerError("authentik was provisioned and the result could not be stored")
	}
	a.useDirectory(result.Settings)

	// Every group this site gives meaning to, in the new directory: the
	// users', and each collective's and organisation's — so people can be put
	// in them from the console straight away.
	if groups, err := a.siteGroups(ctx); err == nil {
		for _, name := range groups.names() {
			a.ensureDirectoryGroup(ctx, name)
		}
	}

	// WARN, and it earns it: this is the moment the site decides which
	// directory administers it.
	log.Warn().Str("instance", result.InstanceURL).Str("first_admin", result.AdminUsername).
		Bool("replacing", in.Body.Reprovision).
		Msg("identity provider configured: the console's administration now answers to it")
	return out, nil
}
