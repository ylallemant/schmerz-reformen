// Package backend is the service that owns the data.
//
// It holds the database, the storage and every write. The console and the
// frontend reach all of it through this service's API and hold credentials to
// nothing else, which is what lets them ship as near-empty images.
package backend

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/ylallemant/schmerz-reformen/internal/cache"
	"github.com/ylallemant/schmerz-reformen/internal/cli"
	"github.com/ylallemant/schmerz-reformen/internal/config"
	"github.com/ylallemant/schmerz-reformen/internal/geocode"
	"github.com/ylallemant/schmerz-reformen/internal/passkey"
	"github.com/ylallemant/schmerz-reformen/internal/push"
	"github.com/ylallemant/schmerz-reformen/internal/ratelimit"
	"github.com/ylallemant/schmerz-reformen/internal/secret"
	"github.com/ylallemant/schmerz-reformen/internal/service"
	"github.com/ylallemant/schmerz-reformen/internal/storage"
	"github.com/ylallemant/schmerz-reformen/internal/store"
)

// Default ports follow the house convention: 8xxx public, 7xxx internal
// tools, 9xxx maintenance. Every service uses a distinct pair so all three run
// side by side in development — and beside the other projects on the same
// machine, which is why these are not the numbers another of them uses.
//
// The backend is not publicly exposed, so it sits in the 7xxx tools range.
const (
	DefaultAppPort         = 7500
	DefaultMaintenancePort = 9500
)

// What the signup limiter allows.
//
// Ten ceremonies at once and ten a minute sustained, per address. The burst
// matters more than the rate: the honest case is bursty — a cancelled
// fingerprint prompt, a second attempt, somebody else on the same wifi — and
// the hostile one is steady.
//
// **It bounds the *beginning* of a ceremony and nothing else.** Finishing one
// requires a challenge that a begin already paid for, so counting both would
// halve the real allowance for no gain; and the device-linking poll runs every
// two seconds by design, which any limit worth having would refuse.
const (
	signupBurst  = 10
	signupRefill = 6 * time.Second
)

// Definition describes the backend binary.
func Definition() cli.Definition {
	return cli.Definition{
		Name:  "backend",
		Title: "schMERZ-Reformen API",
		Short: "The schmerz-reformen API and persistence service",
		Long: "The backend owns the database, the storage and every write.\n" +
			"It is not exposed publicly: the console and the frontend reach it over its API.",
		DefaultAppPort:         DefaultAppPort,
		DefaultMaintenancePort: DefaultMaintenancePort,
		RegisterFlags: func(cmd *cobra.Command) {
			config.RegisterDatabaseFlags(cmd)
			config.RegisterStorageFlags(cmd)
			config.RegisterGeocodeFlags(cmd)
			config.RegisterSiteFlags(cmd)
			// Where readers reach this site, which is what a passkey is bound
			// to, plus the VAPID pair notifications are signed with.
			config.RegisterAuthFlags(cmd)
			// What makes the console believable, and who administers.
			config.RegisterStaffFlags(cmd)
			// What the identity provider's stored credentials are sealed with.
			config.RegisterSecretFlags(cmd)
			config.RegisterNotificationFlags(cmd)
		},
		Setup: Setup,
	}
}

// Command returns the backend's root command.
func Command(version, commit string) *cobra.Command {
	return cli.Build(Definition(), version, commit)
}

// API holds the backend's dependencies.
type API struct {
	store *store.Store

	// storage holds the uploaded logos. Named for its role: what is behind it
	// is a deployment decision.
	storage storage.Store

	// geocoder names the place a pinned point falls in. It is nil when
	// reverse geocoding is switched off, and every call site tolerates that:
	// a location without a name is still a location.
	geocoder geocode.Geocoder

	// siteURL is where the public frontend answers. It is what a push
	// notification links to.
	siteURL string

	// passkeys is this site's WebAuthn relying party.
	passkeys *passkey.Service

	// push delivers the tap on the shoulder. Nil when no VAPID keys are
	// configured, which is not a failure state — the notification list is the
	// channel and everything is written to it either way.
	push *push.Sender

	// pushKey is the VAPID public key a browser needs in order to subscribe.
	// Empty when push is off.
	pushKey string

	// phrases are the few words a notification needs that are not somebody's
	// content: "cancelled", "moved".
	phrases phrases

	// cache holds the answers that are the same for every reader: the map,
	// the feed, the calendar, the list of collectives.
	//
	// It is here rather than in the services that read, because this is the
	// one that owns every write — which is what lets it invalidate rather
	// than expire.
	//
	// **Nothing account-scoped is ever put in it.** See internal/cache.
	cache *cache.Cache

	// signups bounds how often one caller may start a ceremony. An account
	// costs a fingerprint and a second, so this is what stands between a
	// script and ten thousand of them.
	signups *ratelimit.Limiter

	// staffToken is the secret the console presents. Empty switches the
	// content API off unless development is set.
	staffToken string

	// adminGroup is the identity-provider group that may do everything:
	// --admin-group until the console's wizard provisions a directory, and the
	// provisioned group from then on. Read through currentAdminGroup.
	adminMu    sync.RWMutex
	adminGroup string

	// directory is the provisioned Authentik, which the backend reads editors'
	// groups from and manages people in. See directory.go.
	directory *directory

	// provisioned says the console's setup wizard has run. Once it has, an
	// editor's groups come from the directory and never from the header.
	provisioned atomic.Bool

	// development mirrors the --development flag: an editor's identity is
	// believed without a token when none is configured.
	development bool
}

// Setup opens the database, migrates it, and registers the backend's routes.
func Setup(svc *service.Service, common config.Common) error {
	dbConfig, err := config.LoadDatabase()
	if err != nil {
		return fmt.Errorf("database configuration: %w", err)
	}

	// The key the identity provider's credentials are sealed with. Optional,
	// and said either way: a deployment should know which of the two it is.
	secrets, err := secret.New(config.LoadSettingsKey())
	if err != nil {
		return fmt.Errorf("--%s: %w", config.KeySettingsKey, err)
	}
	if secrets.Configured() {
		log.Info().Msg("stored identity-provider credentials are sealed with --settings-key")
	} else {
		log.Warn().Msg("no --settings-key is set: identity-provider credentials are stored in clear, " +
			"so a database backup carries them")
	}

	db, err := store.Open(store.Options{
		Driver:       store.Driver(dbConfig.Driver),
		DSN:          dbConfig.DSN,
		MaxOpenConns: dbConfig.MaxConns,
		Secrets:      secrets,
	})
	if err != nil {
		return err
	}
	svc.OnShutdown(db.Close)

	if err := db.Migrate(); err != nil {
		return err
	}

	storageConfig, err := config.LoadStorage()
	if err != nil {
		return fmt.Errorf("storage configuration: %w", err)
	}
	objects, err := storage.Open(context.Background(), storageConfig.URL)
	if err != nil {
		return err
	}
	svc.OnShutdown(func(context.Context) error { return objects.Close() })

	geocodeConfig := config.LoadGeocode()
	var geocoder geocode.Geocoder
	if geocodeConfig.Enabled {
		geocoder = geocode.New(geocodeConfig.Endpoint,
			geocodeConfig.UserAgent(svc.Version()))
		log.Info().Str("endpoint", geocodeConfig.Endpoint).
			Msg("reverse geocoding enabled")
	}

	// Where readers reach this site. It decides the relying party id, and a
	// passkey is bound to that for ever: it cannot be migrated, because the
	// private halves live on people's devices. Resolved and logged at startup
	// so a wrong answer is visible before anybody registers against it.
	authConfig, err := config.LoadAuth()
	if err != nil {
		return fmt.Errorf("authentication configuration: %w", err)
	}

	passkeys, err := passkey.New(passkey.Options{
		ID:          authConfig.RPID,
		DisplayName: config.LoadSiteName(),
		Origins:     authConfig.Origins,
	})
	if err != nil {
		// Refused here rather than at the first sign-in. A bad relying party
		// id produces an error in somebody's browser that nothing on the
		// server sees, so the only symptom would be that nobody can sign in
		// and nothing is logged.
		return fmt.Errorf("passkeys: %w", err)
	}
	log.Info().Str("relying_party", authConfig.RPID).
		Strs("origins", authConfig.Origins).
		Msg("passkeys bound to this relying party — changing it invalidates every credential")

	// No keys is not a failure. The notification list is the channel and the
	// push is a tap on the shoulder, so a site without one still tells
	// everybody everything — just not before they next open it.
	pushConfig := config.LoadPush()
	var sender *push.Sender
	if pushConfig.Configured() {
		sender, err = push.New(push.Options{
			PublicKey:  pushConfig.PublicKey,
			PrivateKey: pushConfig.PrivateKey,
			Subject:    pushConfig.Subject,
		})
		if err != nil {
			return fmt.Errorf("web push: %w", err)
		}
		if pushConfig.Subject == "" {
			// VAPID requires it, and it is how a provider tells an operator
			// their sending is broken rather than silently dropping it.
			log.Warn().Msg("no --push-subject is set: a push service has no way to reach this operator")
		}
		log.Info().Msg("web push enabled")
	} else {
		log.Info().Msg("web push is off: notifications are written to the list and not delivered")
	}

	staffConfig := config.LoadStaff()
	switch {
	case staffConfig.Token != "":
		log.Info().Str("admin_group", staffConfig.AdminGroup).
			Msg("the content API answers the console, and nobody else")
	case common.Development:
		log.Warn().Str("admin_group", staffConfig.AdminGroup).
			Msg("no --staff-token is set: in development, any caller is believed to be the console")
	default:
		// Not a startup failure. A backend that refused to start would take
		// the public site down over a setting that only the console needs —
		// readers can still read. But nobody can publish, and that has to be
		// said where an operator will see it.
		log.Warn().Msg("no --staff-token is set: the content API refuses every call and the console cannot work")
	}

	api := &API{
		store:       db,
		storage:     objects,
		cache:       cache.New(cache.DefaultTTL, cache.DefaultLimit),
		passkeys:    passkeys,
		push:        sender,
		pushKey:     pushConfig.PublicKey,
		phrases:     phrasesFor(config.LoadNotificationLanguage()),
		signups:     ratelimit.New(signupBurst, signupRefill),
		siteURL:     config.LoadSiteURL(),
		geocoder:    geocoder,
		staffToken:  staffConfig.Token,
		adminGroup:  staffConfig.AdminGroup,
		directory:   &directory{},
		development: common.Development,
	}

	if err := api.seedThemes(context.Background()); err != nil {
		return err
	}
	// Who the console's editors are, once its setup wizard has said where.
	api.connectDirectory(context.Background())
	api.registerRoutes(svc)

	// The backend is only ready while its database is: reporting ready with
	// an unreachable database would send it traffic it cannot serve.
	svc.SetReadinessCheck(db.Ready)

	// Expired challenges, link tokens and sessions are rows nobody will ever
	// read again. The sweep is stopped as part of shutdown so it cannot
	// outlive the database it writes to.
	sweepCtx, stopSweeping := context.WithCancel(context.Background())
	go api.sweepExpired(sweepCtx)
	svc.OnShutdown(func(context.Context) error {
		stopSweeping()
		return nil
	})
	return nil
}

func (a *API) registerRoutes(svc *service.Service) {
	// Authentication and per-caller bounds are middleware rather than a check
	// inside each handler: a route declares what it needs, and a route copied
	// from another cannot forget to enforce it.
	svc.API().UseMiddleware(a.authenticate(svc.API()))
	a.registerOperations(svc.API())
}

// registerOperations puts every operation on an API.
//
// One list, used by the service and by the tests that check every route's
// declarations: a second copy of it in a test would be a list a new route can
// be left out of, and the route would then be checked by nothing.
func (a *API) registerOperations(api huma.API) {
	a.registerAccountRoutes(api)
	a.registerLinkRoutes(api)
	a.registerNotificationRoutes(api)
	a.registerThemeRoutes(api)
	a.registerThemeAssetRoutes(api)

	a.registerCollectiveRoutes(api)
	a.registerOrganisationRoutes(api)
	a.registerEntryPeopleRoutes(api)
	a.registerTopicRoutes(api)
	a.registerUpdateRoutes(api)
	a.registerActionRoutes(api)
	a.registerMapRoutes(api)
	a.registerFollowRoutes(api)
	a.registerMediaRoutes(api)
	a.registerStaffRoutes(api)
	a.registerAuthRoutes(api)
	a.registerPeopleRoutes(api)
}

// sweepInterval is how often expired rows are removed. Nothing depends on the
// sweep for correctness — every read already checks the expiry — so this is
// housekeeping and can be slow.
const sweepInterval = 10 * time.Minute

// sweepExpired removes what has expired: half-finished ceremonies, unclaimed
// device links, and sessions nobody renewed.
func (a *API) sweepExpired(ctx context.Context) {
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		var removed int64
		for name, purge := range map[string]func(context.Context) (int64, error){
			"ceremonies":  a.store.PurgeCeremonies,
			"link tokens": a.store.PurgeLinkTokens,
			"sessions":    a.store.PurgeSessions,
		} {
			count, err := purge(ctx)
			if err != nil {
				log.Error().Err(err).Str("what", name).Msg("cannot remove expired rows")
				continue
			}
			removed += count
		}
		if removed > 0 {
			log.Debug().Int64("rows", removed).Msg("expired rows removed")
		}
	}
}
