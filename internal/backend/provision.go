package backend

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/authentik"
	"github.com/ylallemant/schmerz-reformen/internal/models"
	"github.com/ylallemant/schmerz-reformen/internal/staffauth"
)

// Provisioning is what the setup wizard did, or why it could not.
type Provisioning struct {
	// Done says an identity provider is now configured.
	Done bool

	// Missing is what the instance lacks, in words an operator can act on.
	// Non-empty with Done false means nothing was created.
	Missing []string

	// AdminUsername is who was made the first administrator.
	AdminUsername string

	// TokenOwner is whose API token ran the wizard — on a fresh Authentik the
	// bootstrap `akadmin`, which is why the first admin can be named instead.
	TokenOwner string

	InstanceURL string
	AppName     string

	// RecoveryReady says people can be given a way in.
	RecoveryReady bool

	// RecoveryFlow says what was done about the one flow the wizard builds.
	RecoveryFlow authentik.RecoveryFlow

	// ConsoleToken is the credential the backend keeps, which is **not** the
	// one pasted into the wizard. See authentik.EnsureConsoleToken.
	ConsoleToken authentik.ConsoleToken

	// AdminLink is a way in for the first administrator, shown once.
	AdminLink string

	// DirectoryByPlatform says the backend works with the deployment
	// platform's service account, so no token or account was made here.
	DirectoryByPlatform bool

	// ServiceAccount is the account the backend now works as, and
	// ServiceAccountNote why it does not when it does not.
	ServiceAccount     string
	ServiceAccountNote string

	// Reconciled lists what already existed in the directory and had to be
	// brought back in line — a provider sending editors to an old address, a
	// token somebody made expire. Empty on a first run, and on a re-run that
	// found everything as it should be.
	Reconciled []string

	// Settings is what is stored. It carries both secrets, so it is saved and
	// never returned.
	Settings models.AuthSettings
}

// firstAdmin is who this site answers to, as the wizard was told. Empty means
// the token's owner.
type firstAdmin struct {
	Username string
	Name     string
}

func (f firstAdmin) named() bool { return strings.TrimSpace(f.Username) != "" }

// ensureFirstAdmin finds or creates the person the wizard named. An existing
// account is adopted as it is: a name typed into the wizard does not rewrite
// what their directory already says about them.
func ensureFirstAdmin(ctx context.Context, client *authentik.Client, first firstAdmin) (authentik.User, error) {
	username := strings.TrimSpace(first.Username)

	existing, err := client.UserByUsername(ctx, username)
	switch {
	case err == nil && !existing.IsActive:
		// Deactivated by somebody, in a directory that is not this site's:
		// making it the administrator would overrule that without anybody
		// deciding to.
		return authentik.User{}, fmt.Errorf("%w: %s — reactivate it in Authentik, or name somebody else",
			authentik.ErrInactive, username)
	case err == nil:
		log.Info().Str("username", username).
			Msg("the first admin already exists in the directory; adopting that account")
		return existing, nil
	case !errors.Is(err, authentik.ErrNotFound):
		return authentik.User{}, fmt.Errorf("look for %q: %w", username, err)
	}

	name := strings.TrimSpace(first.Name)
	if name == "" {
		name = username
	}
	made, err := client.CreateUser(ctx, authentik.UserSpec{Username: username, Name: name})
	if err != nil {
		return authentik.User{}, fmt.Errorf("create the first admin %q: %w", username, err)
	}
	return made, nil
}

// provision creates this site's own objects in somebody else's directory, and
// nothing else.
//
// # The order is deliberate
//
// The token is proved first, by asking who it belongs to. Then what the
// instance already has is checked — the flows a provider needs, and a
// certificate to sign tokens with. Only then is anything created, so an
// operator who typed a URL wrong, or made a token without enough permission,
// finds out before their directory holds half a provider.
//
// # Everything is find-or-create
//
// A wizard that failed halfway must be runnable again. The second attempt
// finds what the first one made rather than colliding with it.
//
// # When the deployment platform made the sign-in
//
// With platformSignIn the console signs people in with a client the platform
// made, and the administrators', users' and waiting groups are the
// platform's: they carry its stamp, and the platform refuses to adopt a
// group of the same name made by anybody else. So none of that is made here —
// the groups are required, by name, and their absence is said rather than
// filled — and what is provisioned is what the platform does not give yet:
// the service account that makes the per-collective and per-organisation
// groups, and the recovery flow, bound to the brand of the address this
// site's people use.
//
// # The one flow it builds
//
// Flows are instance-wide and shared with every other application in the
// operator's directory, so the wizard creates none — except a recovery flow,
// because Authentik ships none and a recovery link is the only way this site
// hands an account over. See authentik.EnsureRecoveryFlow.
//
// # When the platform also gives the backend its directory access
//
// With platformDirectory the backend reaches the directory with a service
// account the platform made and a token from its environment. Setup then
// mints no token and makes no account: the platform owns both, and sets the
// token back to its own value on every reconcile, so a token stored here would
// stop working the moment it did. What is left is the operator's part — the
// recovery flow bound to this site's brand, and the first administrator.
func provision(ctx context.Context, client *authentik.Client, consoleURL, appName string,
	first firstAdmin, platformSignIn, platformDirectory bool) (Provisioning, error) {
	var result Provisioning
	result.InstanceURL = client.InstanceURL()
	result.AppName = appName

	if !models.ValidAppName(appName) {
		return result, fmt.Errorf(
			"%q is not a usable name: lowercase letters, digits, and - or _ inside", appName)
	}
	adminGroupName := models.AdminGroupName(appName)

	me, err := client.Me(ctx)
	if err != nil {
		return result, fmt.Errorf("the token was refused: %w", err)
	}
	if me.Username == "" {
		return result, errors.New("authentik did not say who this token belongs to")
	}
	result.AdminUsername = me.Username
	result.TokenOwner = me.Username

	found, err := client.Check(ctx)
	if err != nil {
		return result, fmt.Errorf("check what the instance has: %w", err)
	}
	result.Missing = found.Missing

	// The platform's groups, required rather than made, and before anything
	// is created: one made here would carry no stamp, and the platform's next
	// reconcile would refuse it as "made by hand".
	var platformAdmins authentik.Group
	if platformSignIn {
		result.Missing = nil // the flows a provider needs are not this run's concern
		for i, name := range []string{adminGroupName, models.UsersGroupName(appName), models.PendingGroupName(appName)} {
			group, err := client.GroupByName(ctx, name)
			switch {
			case errors.Is(err, authentik.ErrNotFound):
				result.Missing = append(result.Missing, "the group "+name+
					", which the deployment platform makes: run its identity reconcile first")
			case err != nil:
				return result, fmt.Errorf("find the group %s: %w", name, err)
			case i == 0:
				platformAdmins = group
			}
		}
		if len(result.Missing) > 0 {
			log.Warn().Strs("missing", result.Missing).
				Msg("the platform's groups are not there yet; nothing was created")
			return result, nil
		}
	}

	if found.HasRecoveryFlow {
		result.RecoveryReady = true
	} else {
		recovery, err := client.EnsureRecoveryFlow(ctx, appName)
		if err != nil {
			// Not fatal: everything else can still be provisioned, and an
			// operator who builds their own flow afterwards has a working
			// console. Said rather than swallowed.
			log.Error().Err(err).Msg("cannot build a recovery flow; nobody can be onboarded yet")
		} else {
			result.RecoveryFlow = recovery
			result.Reconciled = append(result.Reconciled, recovery.Repaired...)
			result.RecoveryReady = recovery.BoundToBrand
			if !recovery.BoundToBrand {
				result.Missing = append(result.Missing,
					"the default brand to use a recovery flow — this site built "+
						recovery.Slug+", and the brand already names another one")
			}
		}
	}

	var provider authentik.Provider
	var admins authentik.Group
	if platformSignIn {
		admins = platformAdmins
	} else {
		// What the ID token will be signed with, before anything is created:
		// a provider with no signing key signs HS256 with the client secret,
		// and the console cannot verify it. The first live run of this
		// integration in doléances provisioned a complete directory and then
		// failed every sign-in with `unexpected signature algorithm "HS256"`.
		signingKey, err := client.SigningKey(ctx)
		switch {
		case errors.Is(err, authentik.ErrNotFound):
			result.Missing = append(result.Missing,
				"a certificate with a private key, which the provider needs to sign tokens")
		case err != nil:
			return result, fmt.Errorf("find a signing certificate: %w", err)
		}

		if !found.Ready() || signingKey == "" {
			log.Warn().Strs("missing", result.Missing).
				Msg("authentik is missing what a provider needs; nothing was created")
			return result, nil
		}

		// No `email`: the console has no use for an editor's address, so
		// its tokens do not carry one.
		scopes, err := client.ScopeMappings(ctx, "openid", "profile")
		if err != nil {
			return result, fmt.Errorf("read the scope mappings: %w", err)
		}

		provider, err = client.EnsureProvider(ctx, authentik.ProviderSpec{
			Name:              appName + " console",
			RedirectURI:       strings.TrimSuffix(consoleURL, "/") + staffauth.CallbackPath,
			AuthorizationFlow: found.AuthorizationFlow,
			InvalidationFlow:  found.InvalidationFlow,
			ScopeMappings:     scopes,
			SigningKey:        signingKey,
		})
		if err != nil {
			return result, err
		}
		result.Reconciled = append(result.Reconciled, provider.Repaired...)
		app, err := client.EnsureApplication(ctx, appName+" console", appName, provider)
		if err != nil {
			return result, err
		}
		result.Reconciled = append(result.Reconciled, app.Repaired...)

		if admins, err = client.EnsureGroup(ctx, adminGroupName); err != nil {
			return result, err
		}
	}

	admin := me
	if first.named() {
		admin, err = ensureFirstAdmin(ctx, client, first)
		if err != nil {
			return result, err
		}
		result.AdminUsername = admin.Username
	}
	if err := client.AddToGroup(ctx, admins.PK, admin.PK); err != nil {
		return result, fmt.Errorf("put %s in %s: %w", admin.Username, adminGroupName, err)
	}

	var apiToken string
	if platformDirectory {
		// The platform's account, from the backend's environment: nothing to
		// mint, and nothing to store.
		result.DirectoryByPlatform = true
	} else {
		// The credential the backend keeps from here. The pasted token is a
		// bootstrap credential that Authentik expires in thirty minutes by
		// default; keeping it would make a working installation refuse every
		// editor half an hour later.
		keep, err := client.EnsureConsoleToken(ctx, appName, me.PK)
		if err != nil {
			return result, fmt.Errorf("mint this site's own token: %w", err)
		}
		result.ConsoleToken = keep
		result.Reconciled = append(result.Reconciled, keep.Repaired...)
		apiToken = keep.Key

		// And the site's own account, so it does not depend on the person
		// who ran this staying in the directory with today's rights.
		// Switched to only once it exists, holds its role and its token
		// answered; anything less keeps the token just minted, and says why.
		service, err := client.EnsureServiceAccount(ctx, appName)
		switch {
		case err != nil:
			log.Error().Err(err).Msg("cannot set up the service account; keeping the operator's token")
			result.ServiceAccountNote = "the service account could not be set up: " + err.Error()
		case service.Ready:
			apiToken = service.Key
			result.ServiceAccount = service.Username
			result.Reconciled = append(result.Reconciled, service.Repaired...)
		default:
			result.ServiceAccountNote = service.NotUsed
		}
	}

	// The group of everybody who may use the console, so the first people
	// can be added to it straight away, and the one self-made accounts wait
	// in — not a role, see onboarding.go. The platform's, when it made the
	// sign-in: found above.
	if !platformSignIn {
		if _, err := client.EnsureGroup(ctx, models.UsersGroupName(appName)); err != nil {
			return result, err
		}
		if _, err := client.EnsureGroup(ctx, models.PendingGroupName(appName)); err != nil {
			return result, err
		}
	}

	if result.RecoveryReady {
		if link, err := client.RecoveryLink(ctx, admin.PK); err != nil {
			log.Error().Err(err).Str("username", admin.Username).
				Msg("no way in could be minted for the first admin")
			result.Missing = append(result.Missing,
				"a way in for "+admin.Username+" — the account exists and administers this site, "+
					"and a link has to be minted from the console's people page before they can sign in")
		} else {
			result.AdminLink = link
		}
	}

	result.Done = true
	result.Settings = models.AuthSettings{
		Provisioned:   true,
		AppName:       appName,
		InstanceURL:   client.InstanceURL(),
		ConsoleURL:    strings.TrimSuffix(consoleURL, "/"),
		ClientID:      provider.ClientID,
		ClientSecret:  provider.ClientSecret,
		APIToken:      apiToken,
		AdminGroup:    admins.PK,
		ProvisionedBy: admin.Username,

		SignInByPlatform:    platformSignIn,
		DirectoryByPlatform: platformDirectory,
	}

	log.Warn().
		Str("instance", client.InstanceURL()).
		Str("first_admin", admin.Username).
		Str("provisioned_with", me.Username).
		Msg("authentik provisioned: this site's administration now answers to that directory")
	return result, nil
}
