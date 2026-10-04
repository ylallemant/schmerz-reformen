package backend

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeDirectory is a stand-in Authentik that keeps state, so a test can
// provision against it, then sign somebody in, change their groups and watch
// the backend notice.
//
// **It proves what the backend sends and how it reads the answers, not how
// Authentik behaves.** The shapes are the ones internal/authentik reads, which
// came from Authentik's own schema. It answers only the endpoints this site
// calls.
type fakeDirectory struct {
	t      *testing.T
	server *httptest.Server

	mu sync.Mutex

	// Instance state.
	recoveryBound bool
	hasCert       bool

	users  map[int]*fakeUser
	groups map[string]*fakeGroup // by pk
	nextPK int

	providers    []map[string]any
	applications []map[string]any
	flows        []map[string]any
	tokens       map[string]map[string]any
	objects      map[string][]map[string]any // stages, prompts, bindings by path

	// calls records method and path; bodies the last body sent to each.
	calls  []string
	bodies map[string]map[string]any

	// refuseToken makes every call answer 403, as an expired token does.
	refuseToken bool
}

type fakeUser struct {
	PK       int
	Username string
	Name     string
	Active   bool
	Groups   []string // pks
}

type fakeGroup struct {
	PK   string
	Name string
}

func newFakeDirectory(t *testing.T) *fakeDirectory {
	t.Helper()
	f := &fakeDirectory{
		t: t, hasCert: true,
		users:  map[int]*fakeUser{1: {PK: 1, Username: "akadmin", Name: "authentik Default Admin", Active: true}},
		groups: map[string]*fakeGroup{}, nextPK: 100,
		flows: []map[string]any{
			{"pk": "flow-authz", "slug": "default-provider-authorization-implicit-consent", "designation": "authorization"},
			{"pk": "flow-invalidate", "slug": "default-provider-invalidation-flow", "designation": "invalidation"},
		},
		tokens:  map[string]map[string]any{},
		objects: map[string][]map[string]any{},
		bodies:  map[string]map[string]any{},
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeDirectory) URL() string { return f.server.URL }

func (f *fakeDirectory) id() int {
	f.nextPK++
	return f.nextPK
}

// addUser puts somebody in the directory, in the named groups.
func (f *fakeDirectory) addUser(username string, groups ...string) *fakeUser {
	f.mu.Lock()
	defer f.mu.Unlock()
	user := &fakeUser{PK: f.id(), Username: username, Name: username, Active: true}
	for _, name := range groups {
		user.Groups = append(user.Groups, f.groupNamed(name).PK)
	}
	f.users[user.PK] = user
	return user
}

// groupNamed finds or makes a group. The caller holds the lock.
func (f *fakeDirectory) groupNamed(name string) *fakeGroup {
	for _, group := range f.groups {
		if group.Name == name {
			return group
		}
	}
	group := &fakeGroup{PK: "uuid-" + name, Name: name}
	f.groups[group.PK] = group
	return group
}

// accountsNamed counts the accounts with a username.
func (f *fakeDirectory) accountsNamed(username string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, user := range f.users {
		if user.Username == username {
			count++
		}
	}
	return count
}

// deactivate switches somebody off, as an operator would in Authentik.
func (f *fakeDirectory) deactivate(username string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, user := range f.users {
		if user.Username == username {
			user.Active = false
		}
	}
}

// provider is the first provider, as the directory now has it.
func (f *fakeDirectory) provider() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.providers) == 0 {
		return nil
	}
	return f.providers[0]
}

// setGroups replaces somebody's groups, as an operator would in Authentik.
func (f *fakeDirectory) setGroups(username string, groups ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, user := range f.users {
		if user.Username == username {
			user.Groups = nil
			for _, name := range groups {
				user.Groups = append(user.Groups, f.groupNamed(name).PK)
			}
		}
	}
}

// groupsOf names somebody's groups.
func (f *fakeDirectory) groupsOf(username string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var names []string
	for _, user := range f.users {
		if user.Username == username {
			for _, pk := range user.Groups {
				names = append(names, f.groups[pk].Name)
			}
		}
	}
	return names
}

func (f *fakeDirectory) called(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, call := range f.calls {
		if strings.HasPrefix(call, prefix) {
			count++
		}
	}
	return count
}

func (f *fakeDirectory) body(key string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[key]
}

func page(results []any) map[string]any {
	if results == nil {
		results = []any{}
	}
	return map[string]any{"pagination": map[string]any{"next": 0, "count": len(results)}, "results": results}
}

func (f *fakeDirectory) userJSON(user *fakeUser) map[string]any {
	groups := []string{}
	objs := []map[string]any{}
	for _, pk := range user.Groups {
		groups = append(groups, pk)
		objs = append(objs, map[string]any{"pk": pk, "name": f.groups[pk].Name})
	}
	return map[string]any{
		"pk": user.PK, "username": user.Username, "name": user.Name, "is_active": user.Active,
		"groups": groups, "groups_obj": objs,
	}
}

func (f *fakeDirectory) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	path := strings.TrimPrefix(r.URL.Path, "/api/v3")
	f.calls = append(f.calls, r.Method+" "+path)
	var body map[string]any
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
		if body != nil {
			f.bodies[r.Method+" "+path] = body
		}
	}
	answer := func(status int, value any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if value != nil {
			json.NewEncoder(w).Encode(value) //nolint:errcheck
		}
	}
	if f.refuseToken {
		answer(http.StatusForbidden, map[string]any{"detail": "Token invalid/expired"})
		return
	}
	query := r.URL.Query()

	switch {
	case r.Method == http.MethodGet && path == "/core/users/me/":
		answer(200, map[string]any{"user": map[string]any{
			"pk": 1, "username": "akadmin", "name": "authentik Default Admin", "is_active": true,
			"groups": []map[string]any{},
		}})

	case r.Method == http.MethodGet && path == "/core/users/":
		var results []any
		for _, user := range f.users {
			if name := query.Get("username"); name != "" && user.Username != name {
				continue
			}
			if group := query.Get("groups_by_name"); group != "" {
				in := false
				for _, pk := range user.Groups {
					in = in || f.groups[pk].Name == group
				}
				if !in {
					continue
				}
			}
			results = append(results, f.userJSON(user))
		}
		answer(200, page(results))

	case r.Method == http.MethodPost && path == "/core/users/":
		user := &fakeUser{PK: f.id(), Username: body["username"].(string), Name: body["name"].(string), Active: true}
		if groups, ok := body["groups"].([]any); ok {
			for _, pk := range groups {
				user.Groups = append(user.Groups, pk.(string))
			}
		}
		f.users[user.PK] = user
		answer(201, f.userJSON(user))

	case r.Method == http.MethodPost && strings.HasSuffix(path, "/recovery/"):
		if !f.recoveryBound {
			answer(400, map[string]any{"non_field_errors": []string{"No recovery flow set."}})
			return
		}
		pk := strings.TrimSuffix(strings.TrimPrefix(path, "/core/users/"), "/recovery/")
		answer(200, map[string]any{"link": f.server.URL + "/if/flow/recovery/?token=for-" + pk})

	case r.Method == http.MethodGet && path == "/core/groups/":
		var results []any
		for _, group := range f.groups {
			if name := query.Get("name"); name == "" || group.Name == name {
				results = append(results, map[string]any{"pk": group.PK, "name": group.Name})
			}
		}
		answer(200, page(results))

	case r.Method == http.MethodPost && path == "/core/groups/":
		group := f.groupNamed(body["name"].(string))
		answer(201, map[string]any{"pk": group.PK, "name": group.Name})

	case r.Method == http.MethodPost && strings.HasSuffix(path, "/add_user/"),
		r.Method == http.MethodPost && strings.HasSuffix(path, "/remove_user/"):
		groupPK := strings.Split(strings.TrimPrefix(path, "/core/groups/"), "/")[0]
		user := f.users[int(body["pk"].(float64))]
		if user == nil {
			answer(404, nil)
			return
		}
		kept := []string{}
		for _, pk := range user.Groups {
			if pk != groupPK {
				kept = append(kept, pk)
			}
		}
		if strings.HasSuffix(path, "/add_user/") {
			kept = append(kept, groupPK)
		}
		user.Groups = kept
		answer(204, nil)

	case r.Method == http.MethodGet && path == "/flows/instances/":
		var results []any
		for _, flow := range f.flows {
			if slug := query.Get("slug"); slug != "" && flow["slug"] != slug {
				continue
			}
			if designation := query.Get("designation"); designation != "" && flow["designation"] != designation {
				continue
			}
			results = append(results, flow)
		}
		answer(200, page(results))

	case r.Method == http.MethodPost && path == "/flows/instances/":
		flow := map[string]any{"pk": "flow-" + body["slug"].(string), "slug": body["slug"], "designation": body["designation"]}
		f.flows = append(f.flows, flow)
		answer(201, flow)

	case r.Method == http.MethodGet && path == "/core/brands/":
		recovery := ""
		if f.recoveryBound {
			recovery = "flow-recovery"
		}
		answer(200, page([]any{map[string]any{"brand_uuid": "brand-1", "default": true, "flow_recovery": recovery}}))

	case r.Method == http.MethodPatch && path == "/core/brands/brand-1/":
		f.recoveryBound = true
		answer(200, map[string]any{})

	case r.Method == http.MethodGet && path == "/stages/all/":
		answer(200, page([]any{map[string]any{
			"pk": "stage-webauthn", "name": "default-authenticator-webauthn-setup",
			"component": "ak-stage-authenticator-webauthn-form",
		}}))

	case r.Method == http.MethodGet && path == "/crypto/certificatekeypairs/":
		if !f.hasCert {
			answer(200, page(nil))
			return
		}
		answer(200, page([]any{map[string]any{"pk": "cert-1", "name": "authentik Self-signed Certificate"}}))

	case r.Method == http.MethodGet && path == "/propertymappings/provider/scope/":
		answer(200, page([]any{
			map[string]any{"pk": "scope-openid", "scope_name": "openid"},
			map[string]any{"pk": "scope-profile", "scope_name": "profile"},
			map[string]any{"pk": "scope-email", "scope_name": "email"},
		}))

	case r.Method == http.MethodGet && path == "/providers/oauth2/":
		var results []any
		for _, provider := range f.providers {
			if provider["name"] == query.Get("name") {
				results = append(results, provider)
			}
		}
		answer(200, page(results))

	case r.Method == http.MethodPost && path == "/providers/oauth2/":
		provider := map[string]any{
			"pk": f.id(), "client_id": "client-of-" + fmt.Sprint(body["name"]),
			"client_secret": "secret-of-the-provider",
		}
		for key, value := range body {
			provider[key] = value
		}
		f.providers = append(f.providers, provider)
		answer(201, provider)

	case r.Method == http.MethodPatch && strings.HasPrefix(path, "/providers/oauth2/"):
		for _, provider := range f.providers {
			if fmt.Sprint(provider["pk"]) == strings.Trim(strings.TrimPrefix(path, "/providers/oauth2/"), "/") {
				for key, value := range body {
					provider[key] = value
				}
				answer(200, provider)
				return
			}
		}
		answer(404, nil)

	case r.Method == http.MethodPatch && strings.HasPrefix(path, "/core/applications/"):
		for _, app := range f.applications {
			if app["slug"] == strings.Trim(strings.TrimPrefix(path, "/core/applications/"), "/") {
				for key, value := range body {
					app[key] = value
				}
				answer(200, app)
				return
			}
		}
		answer(404, nil)

	case r.Method == http.MethodGet && path == "/core/applications/":
		var results []any
		for _, app := range f.applications {
			if app["slug"] == query.Get("slug") {
				results = append(results, app)
			}
		}
		answer(200, page(results))

	case r.Method == http.MethodPost && path == "/core/applications/":
		app := map[string]any{"pk": "app-" + fmt.Sprint(body["slug"]), "name": body["name"], "slug": body["slug"], "provider": body["provider"]}
		f.applications = append(f.applications, app)
		answer(201, app)

	case r.Method == http.MethodGet && path == "/core/tokens/":
		var results []any
		if token, ok := f.tokens[query.Get("identifier")]; ok {
			results = append(results, token)
		}
		answer(200, page(results))

	case r.Method == http.MethodPost && path == "/core/tokens/":
		identifier := body["identifier"].(string)
		f.tokens[identifier] = map[string]any{
			"identifier": identifier, "intent": body["intent"], "expiring": body["expiring"],
		}
		answer(201, f.tokens[identifier])

	case r.Method == http.MethodPatch && strings.HasPrefix(path, "/core/tokens/"):
		token := f.tokens[strings.Trim(strings.TrimPrefix(path, "/core/tokens/"), "/")]
		if token == nil {
			answer(404, nil)
			return
		}
		for key, value := range body {
			token[key] = value
		}
		answer(200, token)

	case r.Method == http.MethodGet && strings.HasPrefix(path, "/core/tokens/") && strings.HasSuffix(path, "/view_key/"):
		answer(200, map[string]any{"key": "the-backends-own-token"})

	case r.Method == http.MethodGet && (strings.HasPrefix(path, "/stages/") || path == "/flows/bindings/"):
		var results []any
		for _, object := range f.objects[path] {
			results = append(results, object)
		}
		answer(200, page(results))

	case r.Method == http.MethodPost && (strings.HasPrefix(path, "/stages/") || path == "/flows/bindings/"):
		object := map[string]any{"pk": "obj-" + strconv.Itoa(f.id())}
		for key, value := range body {
			object[key] = value
		}
		f.objects[path] = append(f.objects[path], object)
		answer(201, object)

	default:
		f.t.Errorf("the fake directory was asked something it does not model: %s %s", r.Method, path)
		answer(404, nil)
	}
}
