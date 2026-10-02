package backend

import (
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

// TestRoutesRegister builds the whole API surface.
//
// It exists because huma.Register panics on a malformed operation rather than
// returning an error — a pointer query parameter, a duplicated path, an input
// type it cannot reflect — and none of that is visible to the compiler. The
// first sign is the backend dying at startup, which every other test can pass
// through without noticing.
func TestRoutesRegister(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("registering the API panicked: %v", recovered)
		}
	}()
	everyOperation(t)
}

// registerEverything puts the whole API surface on one adapter.
//
// Shared by every test that walks the registered operations. Two lists of
// register calls would mean a route added to one and missing from the other,
// which is exactly the hole those tests exist to close.
func registerEverything(a *API, api huma.API) {
	a.registerAccountRoutes(api)
	a.registerLinkRoutes(api)
	a.registerNotificationRoutes(api)
	a.registerThemeRoutes(api)
	a.registerThemeAssetRoutes(api)
	a.registerCollectiveRoutes(api)
	a.registerTopicRoutes(api)
	a.registerUpdateRoutes(api)
	a.registerActionRoutes(api)
	a.registerMapRoutes(api)
	a.registerFollowRoutes(api)
	a.registerMediaRoutes(api)
	a.registerStaffRoutes(api)
}

// route is one registered operation with where it answers.
type route struct {
	path string
	op   *huma.Operation
}

// everyOperation registers the API and returns every operation on it.
func everyOperation(t *testing.T) []route {
	t.Helper()

	api := humago.New(http.NewServeMux(), huma.DefaultConfig("schMERZ-Reformen", "test"))
	registerEverything(&API{}, api)

	var routes []route
	for path, item := range api.OpenAPI().Paths {
		for _, op := range []*huma.Operation{item.Get, item.Post, item.Put, item.Patch, item.Delete} {
			if op != nil {
				routes = append(routes, route{path: path, op: op})
			}
		}
	}
	sort.Slice(routes, func(i, j int) bool {
		return routes[i].path+routes[i].op.Method < routes[j].path+routes[j].op.Method
	})
	return routes
}

// TestEveryConsoleRouteIsDeclaredAsOne is the test that stops the next
// content endpoint from being reachable by a reader.
//
// The check lives in middleware and the requirement lives on the operation,
// so a route that forgot to say staffOnly is a route with no check at all —
// and nothing about its handler would look wrong, because the handlers call
// mustStaff, which a missing declaration turns into a plain 401 only by luck
// of the handler having remembered. Here the path is the contract: anything
// under /v1/staff/ declares itself, and nothing else does.
func TestEveryConsoleRouteIsDeclaredAsOne(t *testing.T) {
	for _, route := range everyOperation(t) {
		declared, _ := route.op.Metadata[requiresStaff].(bool)
		under := strings.HasPrefix(route.path, "/v1/staff/")

		switch {
		case under && !declared:
			t.Errorf("%s %s is under /v1/staff/ and is not declared staffOnly: a reader could call it",
				route.op.Method, route.path)
		case declared && !under && !isThemeLibraryRoute(route.path):
			t.Errorf("%s %s is declared staffOnly outside /v1/staff/: the path no longer says who it is for",
				route.op.Method, route.path)
		}
	}
}

// isThemeLibraryRoute recognises the theme library, which keeps the paths it
// has in the project it was brought over from. Its reads that every page needs
// — the stylesheet, the active images, a package file — are public; the
// library itself is the console's.
func isThemeLibraryRoute(path string) bool {
	return strings.HasPrefix(path, "/v1/themes") || strings.HasPrefix(path, "/v1/theme-assets/")
}

// TestNoWriteIsOpenToTheWorld: every route that changes something is either a
// signed-in reader's or the console's. An undeclared write is one anybody who
// can reach the backend may call.
//
// The ceremonies are the exception and are named: a sign-up and a sign-in are
// by definition made by somebody who is not signed in yet, and what bounds
// them is the rate limit on beginning one and the challenge that finishing
// one has to answer.
func TestNoWriteIsOpenToTheWorld(t *testing.T) {
	unauthenticatedByDesign := map[string]bool{
		"begin-signup":        true,
		"finish-signup":       true,
		"begin-signin":        true,
		"finish-signin":       true,
		"use-recovery-code":   true,
		"claim-device-link":   true,
		"poll-device-link":    true,
		"begin-link-passkey":  true,
		"finish-link-passkey": true,
	}

	for _, route := range everyOperation(t) {
		if route.op.Method == http.MethodGet {
			continue
		}
		session, _ := route.op.Metadata[requiresSession].(bool)
		console, _ := route.op.Metadata[requiresStaff].(bool)
		if session || console || unauthenticatedByDesign[route.op.OperationID] {
			continue
		}
		t.Errorf("%s %s (%s) changes something and requires nobody",
			route.op.Method, route.path, route.op.OperationID)
	}
}

// TestParseBounds covers the viewport parameter, which arrives from a browser
// and so is arbitrary text until proven otherwise.
func TestParseBounds(t *testing.T) {
	box, err := parseBounds("51.35,51.12,6.94,6.69")
	if err != nil {
		t.Fatalf("parseBounds: %v", err)
	}
	if box.North != 51.35 || box.South != 51.12 || box.East != 6.94 || box.West != 6.69 {
		t.Errorf("parsed %+v", box)
	}

	for _, bad := range []string{
		"",                      // empty
		"51.3,51.1,6.9",         // three edges describe no box
		"51.3,51.1,6.9,6.6,0",   // five
		"north,south,east,west", // words
		"51.12,51.35,6.94,6.69", // north below south
		"95,-95,10,-10",         // off the planet
	} {
		if _, err := parseBounds(bad); err == nil {
			t.Errorf("parseBounds(%q) was accepted", bad)
		}
	}

	// Absent is everywhere, which is not an error.
	if box, err := optionalBounds("  "); err != nil || box != nil {
		t.Errorf("an absent viewport = %v, %v; want no box and no error", box, err)
	}
}
