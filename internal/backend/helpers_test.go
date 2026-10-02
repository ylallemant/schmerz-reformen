package backend

import (
	"context"
	"crypto/rand"
	"path/filepath"
	"testing"
	"time"

	"github.com/ylallemant/schmerz-reformen/internal/cache"
	"github.com/ylallemant/schmerz-reformen/internal/config"
	"github.com/ylallemant/schmerz-reformen/internal/models"
	"github.com/ylallemant/schmerz-reformen/internal/staffauth"
	"github.com/ylallemant/schmerz-reformen/internal/storage"
	"github.com/ylallemant/schmerz-reformen/internal/store"
)

// newAPI assembles a backend over a fresh SQLite file and a storage directory
// of its own.
//
// With a cache, so every test in this package exercises the cached read paths
// rather than a configuration nothing ships with. Without a geocoder, so no
// test ever asks a third party to name a place.
func newAPI(t *testing.T) *API {
	t.Helper()

	db, err := store.Open(store.Options{
		Driver: store.DriverSQLite,
		DSN:    filepath.Join(t.TempDir(), "test.db"),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close(context.Background()) }) //nolint:errcheck

	if err := db.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	objects, err := storage.Open(context.Background(), "file://"+t.TempDir())
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { objects.Close() }) //nolint:errcheck

	return &API{
		store:      db,
		storage:    objects,
		cache:      cache.New(cache.DefaultTTL, cache.DefaultLimit),
		phrases:    phrasesFor("de"),
		adminGroup: config.DefaultAdminGroup,
		siteURL:    "http://localhost:8401",
	}
}

// admin is somebody in the administrators' group.
func admin() context.Context {
	return asStaff("sub-admin", "Alex Admin", config.DefaultAdminGroup)
}

// editor is somebody in the named groups and no others.
func editor(name string, groups ...string) context.Context {
	return asStaff("sub-"+name, name, groups...)
}

// asStaff is a request context as the middleware leaves it for a console
// route: the identity resolved and the administrator question answered.
func asStaff(subject, name string, groups ...string) context.Context {
	who := &staff{Identity: staffauth.Identity{Subject: subject, Name: name, Groups: groups}}
	for _, group := range groups {
		if group == config.DefaultAdminGroup {
			who.Admin = true
		}
	}
	return context.WithValue(context.Background(), staffKey, who)
}

// reader makes a signed-in account, the way a passkey registration leaves one.
func reader(t *testing.T, a *API, name string) *caller {
	t.Helper()

	handle := make([]byte, 32)
	if _, err := rand.Read(handle); err != nil {
		t.Fatalf("read randomness: %v", err)
	}
	account := &models.Account{Handle: handle, Name: name}
	credential := &models.Credential{
		CredentialID: append([]byte("cred-"), handle...),
		PublicKey:    []byte("not a real key"),
		Name:         "a device",
	}
	if err := a.store.CreateAccount(context.Background(), account, credential); err != nil {
		t.Fatalf("CreateAccount(%q): %v", name, err)
	}
	return &caller{Account: *account}
}

// as is a request context for a signed-in reader.
func as(who *caller) context.Context {
	return context.WithValue(context.Background(), callerKey, who)
}

// anonymous is a request context for somebody who is not signed in.
func anonymous() context.Context { return context.Background() }

// founded creates a published collective managed by the given group, through
// the same handler the console calls.
func founded(t *testing.T, a *API, name, group string) CollectiveItem {
	t.Helper()

	in := &CreateCollectiveInput{}
	in.Body.Name = name
	in.Body.AuthGroup = group
	in.Body.Status = string(models.StatusPublished)

	out, err := a.staffCreateCollective(admin(), in)
	if err != nil {
		t.Fatalf("staffCreateCollective(%q): %v", name, err)
	}
	return out.Body
}

// raised creates a published topic under a collective, as one of its editors.
func raised(t *testing.T, a *API, ctx context.Context, collectiveID, title string) TopicItem {
	t.Helper()

	in := &CreateTopicInput{ID: collectiveID}
	in.Body.Title = title
	in.Body.Kind = string(models.TopicCut)
	in.Body.Level = string(models.LevelMunicipal)
	in.Body.Status = string(models.StatusPublished)
	in.Body.Latitude, in.Body.Longitude, in.Body.Zoom = 51.2277, 6.7735, 11
	in.Body.Place = "Düsseldorf"

	out, err := a.staffCreateTopic(ctx, in)
	if err != nil {
		t.Fatalf("staffCreateTopic(%q): %v", title, err)
	}
	return out.Body
}

// announced creates a published action under a collective, starting when said.
func announced(t *testing.T, a *API, ctx context.Context, collectiveID, title string, startsAt time.Time) ActionItem {
	t.Helper()

	in := &CreateActionInput{ID: collectiveID}
	in.Body.Title = title
	in.Body.Kind = string(models.ActionDemonstration)
	in.Body.StartsAt = startsAt.Format(time.RFC3339)
	in.Body.Status = string(models.StatusPublished)
	in.Body.Latitude, in.Body.Longitude, in.Body.Zoom = 51.2259, 6.7724, 18
	in.Body.Place = "vor dem Rathaus"

	out, err := a.staffCreateAction(ctx, in)
	if err != nil {
		t.Fatalf("staffCreateAction(%q): %v", title, err)
	}
	return out.Body
}

// notificationsOf reads what an account has been told.
func notificationsOf(t *testing.T, a *API, who *caller) []models.Notification {
	t.Helper()

	items, _, err := a.store.ListNotifications(context.Background(), who.Account.ID, 50, 0)
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	return items
}
