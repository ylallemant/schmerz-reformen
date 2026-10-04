package backend

import (
	"net/http"
	"testing"

	"github.com/ylallemant/schmerz-reformen/internal/cache"
)

// changesNothingCached is every write that does not declare invalidates(), and
// why each is allowed not to.
//
// The point of the list is that it is a list. A write that makes a cached
// answer wrong and says nothing leaves the site serving yesterday's calendar,
// and nothing about the handler looks wrong — so the test below refuses any
// write this file has not accounted for, and adding an endpoint means either
// declaring what it invalidates or writing down here why it does not.
var changesNothingCached = map[string]string{
	// Credentials and the account itself. An account is a handle, a set of
	// public keys and a name nobody else sees, and none of that is in any
	// cached family.
	//
	// Deleting an account is in here for a reason worth stating: it removes
	// follows and intents, which do change two counts. Neither count is ever
	// cached — both are computed on top of the cached rows for each request —
	// so there is nothing stale to drop. See actionItems and followState.
	"begin-signup":           "credentials",
	"finish-signup":          "credentials",
	"begin-signin":           "credentials",
	"finish-signin":          "credentials",
	"use-recovery-code":      "credentials",
	"sign-out":               "credentials",
	"rename-account":         "a name nothing public shows",
	"delete-account":         "removes follows and intents, whose counts are never cached",
	"begin-add-passkey":      "credentials",
	"finish-add-passkey":     "credentials",
	"rename-passkey":         "credentials",
	"delete-passkey":         "credentials",
	"revoke-session":         "credentials",
	"reissue-recovery-codes": "credentials",
	"open-device-link":       "credentials",
	"approve-device-link":    "credentials",
	"refuse-device-link":     "credentials",
	"claim-device-link":      "credentials",
	"poll-device-link":       "credentials",
	"begin-link-passkey":     "credentials",
	"finish-link-passkey":    "credentials",

	// Account-scoped by construction, so they could never be cached here.
	"subscribe-push":         "one device's endpoint",
	"unsubscribe-push":       "one device's endpoint",
	"read-notification":      "one account's own list",
	"read-all-notifications": "one account's own list",
	"delete-notification":    "one account's own list",

	// What a reader does with a page. Each changes a count and a flag, and
	// neither is in the cache: the count is read fresh on every request and
	// the flag is one person's, which is the one kind of thing the cache must
	// never hold.
	"follow":      "a count read fresh and a flag that is one reader's",
	"unfollow":    "a count read fresh and a flag that is one reader's",
	"participate": "a count read fresh and a flag that is one reader's",
	"withdraw":    "a count read fresh and a flag that is one reader's",

	// The theme library, which has a mechanism of its own in each web service.
	"set-active-theme":   "the theme overlay has its own cache",
	"save-theme":         "the theme overlay has its own cache",
	"delete-theme":       "the theme overlay has its own cache",
	"save-theme-asset":   "the theme overlay has its own cache",
	"delete-theme-asset": "the theme overlay has its own cache",

	// The identity provider: who may use the console. Nothing a reader sees
	// is cached by who edits it.
	"console-provision-auth":    "the console's own sign-in configuration",
	"staff-invite-person":       "the directory, not the site's content",
	"staff-set-person-admin":    "the directory, not the site's content",
	"staff-collective-grant":    "the directory, not the site's content",
	"staff-collective-revoke":   "the directory, not the site's content",
	"staff-organisation-grant":  "the directory, not the site's content",
	"staff-organisation-revoke": "the directory, not the site's content",

	// A new organisation is in no collective's list yet, so nothing cached
	// names it.
	"staff-create-organisation": "listed by nothing cached until a collective adds it",
	"staff-relink-person":       "the directory, not the site's content",
	"staff-remove-person":       "the directory, not the site's content",
}

// TestEveryWriteSaysWhatItMakesWrong is the test that stops the next endpoint
// from forgetting.
//
// Invalidation is the mechanism and the lifetime on each entry is only the
// backstop, so a write that drops nothing is not a slow page — it is a
// calendar still showing an action that was called off, with nothing to show
// anything happened. The declaration sits on the operation where somebody
// adding a route is already reading, and this refuses the route that has
// neither a declaration nor a line in the list above.
func TestEveryWriteSaysWhatItMakesWrong(t *testing.T) {
	for _, route := range everyOperation(t) {
		if route.op.Method == http.MethodGet {
			continue
		}
		if _, declared := route.op.Metadata[invalidated]; declared {
			continue
		}
		if _, accounted := changesNothingCached[route.op.OperationID]; accounted {
			continue
		}
		t.Errorf("%s %s (%s) neither declares invalidates(...) nor is accounted for in changesNothingCached",
			route.op.Method, route.path, route.op.OperationID)
	}
}

// TestNothingIsAccountedForTwice keeps the list above honest in the other
// direction: an entry that also declares invalidates() is a stale note, and
// one naming a route that no longer exists is a note about nothing.
func TestNothingIsAccountedForTwice(t *testing.T) {
	writes := map[string]route{}
	for _, route := range everyOperation(t) {
		if route.op.Method != http.MethodGet {
			writes[route.op.OperationID] = route
		}
	}

	for id := range changesNothingCached {
		found, exists := writes[id]
		if !exists {
			t.Errorf("changesNothingCached names %q, which is not a write route", id)
			continue
		}
		if _, declared := found.op.Metadata[invalidated]; declared {
			t.Errorf("%q both declares invalidates(...) and is excused in changesNothingCached", id)
		}
	}
}

// TestACacheIsAnOptimisationNotADependency: a backend assembled without one
// must answer identically and simply read the database every time. Without
// this the first write path reached by a tool that forgot to build a cache
// panics in middleware.
func TestACacheIsAnOptimisationNotADependency(t *testing.T) {
	var absent *cache.Cache

	absent.Drop(cache.Topics, cache.Map)
	absent.Put("k", cache.Topics, 1)
	absent.Clear()

	if _, found := absent.Get("k"); found {
		t.Error("a cache that holds nothing answered a question")
	}

	loaded := 0
	for range 3 {
		value, err := cache.Fetch(absent, "k", cache.Topics, func() (int, error) {
			loaded++
			return 7, nil
		})
		if err != nil || value != 7 {
			t.Fatalf("Fetch = %d, %v", value, err)
		}
	}
	if loaded != 3 {
		t.Errorf("loaded %d times, want 3: with no cache every read goes to the source", loaded)
	}

	// And a whole backend built without one still answers.
	a := newAPI(t)
	a.cache = nil
	founded(t, a, "Bündnis Düsseldorf", "duesseldorf")
	out, err := a.listCollectives(anonymous(), &BoundsInput{})
	if err != nil {
		t.Fatalf("listCollectives with no cache: %v", err)
	}
	if out.Body.Total != 1 {
		t.Errorf("total = %d, want 1", out.Body.Total)
	}
}
