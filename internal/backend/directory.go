package backend

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/authentik"
	"github.com/ylallemant/schmerz-reformen/internal/models"
)

// The directory: the Authentik instance editors sign in through, once the
// console's setup wizard has provisioned it.
//
// # The backend holds the token, the console never does
//
// Everything that reads or changes the directory happens here. The console
// asks for what it needs — who is somebody, add this person, take that role
// away — and holds no credential to the directory at all, which keeps the
// promise every exposed service in this project makes: it talks to the
// backend and holds credentials to nothing else. The one secret the console
// does receive is the OAuth client secret, because exchanging an authorisation
// code is the console's job: it is the one with the browser.
//
// # Roles come from the directory, never from the token
//
// Once provisioned, the groups an editor arrives with in the identity header
// are ignored: the backend asks Authentik itself. A token — and the cookie the
// console keeps — is a snapshot of the moment somebody signed in, and an
// editor removed from a collective's group an hour ago would otherwise still
// be carrying the claim that they belong to it.

// roleCacheLifetime bounds how long a *read* may rely on what the directory
// said. A write never relies on it: see rolesOf.
const roleCacheLifetime = time.Minute

// directory is the backend's connection to Authentik, which appears when the
// wizard finishes and can change when it is run again.
type directory struct {
	mu       sync.RWMutex
	client   *authentik.Client
	settings models.AuthSettings

	cacheMu sync.Mutex
	cache   map[string]cachedRoles
}

type cachedRoles struct {
	groups []string
	at     time.Time
}

// connect points the backend at a provisioned directory.
func (d *directory) connect(settings models.AuthSettings) error {
	client, err := authentik.New(settings.InstanceURL, settings.APIToken)
	if err != nil {
		return fmt.Errorf("build the directory client: %w", err)
	}

	d.mu.Lock()
	d.client = client
	d.settings = settings
	d.mu.Unlock()

	d.cacheMu.Lock()
	d.cache = map[string]cachedRoles{}
	d.cacheMu.Unlock()
	return nil
}

// connected returns the client and settings, or nil when nothing is
// provisioned yet.
func (d *directory) connected() (*authentik.Client, models.AuthSettings) {
	if d == nil {
		return nil, models.AuthSettings{}
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.client, d.settings
}

// rolesOf is the groups somebody is in, by name, as the directory says now.
//
// fresh asks the directory whatever the cache holds, and is what every write
// does: reading a listing may lean on an answer a minute old, changing
// something may not. Somebody the directory does not know, or has
// deactivated, is in no groups — which is not an error: they are refused the
// way somebody who was never given a role is.
func (d *directory) rolesOf(ctx context.Context, username string, fresh bool) ([]string, error) {
	client, _ := d.connected()
	if client == nil {
		return nil, errors.New("no directory is connected")
	}

	if !fresh {
		d.cacheMu.Lock()
		cached, ok := d.cache[username]
		d.cacheMu.Unlock()
		if ok && time.Since(cached.at) < roleCacheLifetime {
			return cached.groups, nil
		}
	}

	person, err := client.UserByUsername(ctx, username)
	var groups []string
	switch {
	case errors.Is(err, authentik.ErrNotFound):
		groups = []string{}
	case err != nil:
		return nil, err
	case !person.IsActive:
		// Deactivating somebody is how an operator removes them without
		// deleting a record their other applications may depend on.
		groups = []string{}
	default:
		groups = person.GroupNames()
	}

	d.cacheMu.Lock()
	if d.cache == nil {
		d.cache = map[string]cachedRoles{}
	}
	d.cache[username] = cachedRoles{groups: groups, at: time.Now()}
	d.cacheMu.Unlock()
	return groups, nil
}

// forget drops what the cache says about somebody, after their groups were
// changed here — so the next read is not a minute behind a change this very
// backend made.
func (d *directory) forget(username string) {
	d.cacheMu.Lock()
	delete(d.cache, username)
	d.cacheMu.Unlock()
}

// connectDirectory reads the stored settings and, if the wizard has run,
// connects to the directory they describe.
//
// It never stops the backend from starting. A directory that cannot be
// reached, or settings that cannot be decrypted, leave the content API
// refusing the console's editors — which is the locked failure — while the
// public site carries on serving readers.
func (a *API) connectDirectory(ctx context.Context) {
	provisioned, err := a.store.AuthProvisioned(ctx)
	if err != nil {
		log.Error().Err(err).Msg("cannot read whether an identity provider is configured")
		return
	}
	a.provisioned.Store(provisioned)
	if !provisioned {
		log.Warn().Msg("no identity provider is configured: the console's setup wizard has to be run")
		return
	}

	settings, err := a.store.AuthSettings(ctx)
	if err != nil {
		log.Error().Err(err).Msg("the identity provider is configured and its settings cannot be read: " +
			"the console's editors are refused until they can (is --settings-key the one they were written with?)")
		return
	}
	a.useDirectory(settings)
}

// useDirectory switches to a provisioned directory: its client for roles, and
// its administrators' group for who administers.
func (a *API) useDirectory(settings models.AuthSettings) {
	if err := a.directory.connect(settings); err != nil {
		log.Error().Err(err).Msg("cannot connect to the identity provider")
		return
	}
	a.provisioned.Store(true)

	if name := settings.AdminGroupName(); name != "" {
		if current := a.currentAdminGroup(); name != current {
			log.Warn().Str("flag", current).Str("provisioned", name).
				Msg("the administrators' group is the provisioned one, not --admin-group")
		}
		a.setAdminGroup(name)
	}
	log.Info().Str("instance", settings.InstanceURL).Str("application", settings.AppName).
		Msg("identity provider connected: editors' groups are read from it")
}

// setAdminGroup and currentAdminGroup guard the one setting provisioning can
// change while requests are being served.
func (a *API) setAdminGroup(name string) {
	a.adminMu.Lock()
	a.adminGroup = name
	a.adminMu.Unlock()
}

func (a *API) currentAdminGroup() string {
	a.adminMu.RLock()
	defer a.adminMu.RUnlock()
	return a.adminGroup
}

// ensureDirectoryGroup makes sure a collective's group exists in the
// directory, so an administrator can put people in it from the console.
//
// Best effort: the collective is saved either way, and a group that could not
// be created is one an operator can create by hand — so a failure is logged
// rather than refused.
func (a *API) ensureDirectoryGroup(ctx context.Context, name string) {
	client, _ := a.directory.connected()
	if client == nil || name == "" {
		return
	}
	if _, err := client.EnsureGroup(ctx, name); err != nil {
		log.Warn().Err(err).Str("group", name).
			Msg("cannot create a collective's group in the directory; create it by hand")
	}
}
