package authentik

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/rs/zerolog/log"
)

// RecoveryFlow is this site's own way of handing an account over.
//
// # Why the wizard builds one, when it builds no other flow
//
// Everything else this package provisions is the site's own: a provider,
// an application, two groups. Flows were deliberately excluded, because they
// are instance-wide and shared with every other application in somebody's
// directory, and a tool asked to add itself has no business rewriting how
// everybody else signs in.
//
// That rule held until it met a real instance. **authentik ships no recovery
// flow at all** — not a disabled blueprint, none — and its published example
// builds one around email verification, which is the one dependency this
// project removed on purpose. So "the wizard creates no flows" meant an
// operator finished setup and could onboard nobody until they had hand-built a
// flow, and the console's advice was to pick one from a list that was empty.
//
// # What makes this safe, where rewriting a flow would not be
//
// It is **additive and prefixed**. Every object is named for the application,
// so two sites on one authentik cannot collide and nothing already there
// is touched: no existing flow is edited, no stage is rebound, nothing is
// deleted. An operator who prefers their own recovery flow keeps it — see the
// brand rule below.
//
// The one instance-wide effect is the brand binding, which is also what makes
// the recovery link work at all. So it is written **only when the brand has
// none**: a directory that already has a recovery flow has an operator's
// decision in it, and this does not overrule it.
type RecoveryFlow struct {
	PK   string
	Slug string

	// Created says this run made the flow, rather than finding it.
	Created bool

	// BoundToBrand says the default brand now uses it. False means the brand
	// already had one of its own, which is left alone.
	BoundToBrand bool

	// Passkey says the flow ends by enrolling one. False means the instance
	// has no WebAuthn stage and the link stops at a password.
	Passkey bool

	// Repaired lists what a re-run had to bring back in line.
	Repaired []string
}

// webAuthnStage finds the instance's passkey enrolment stage, or "".
//
// Matched on **component** rather than name: the name is an operator's to
// change and `ak-stage-authenticator-webauthn-form` is what the thing is.
func (c *Client) webAuthnStage(ctx context.Context) (string, error) {
	for next := 1; next > 0; {
		var listing page[struct {
			PK        string `json:"pk"`
			Name      string `json:"name"`
			Component string `json:"component"`
		}]
		path := fmt.Sprintf("/stages/all/?page=%d&page_size=100", next)
		if err := c.do(ctx, http.MethodGet, path, nil, &listing); err != nil {
			return "", fmt.Errorf("look for a passkey enrolment stage: %w", err)
		}
		for _, stage := range listing.Results {
			if stage.Component == "ak-stage-authenticator-webauthn-form" {
				log.Debug().Str("stage", stage.Name).
					Msg("authentik: enrolling passkeys through this stage")
				return stage.PK, nil
			}
		}
		next = listing.Pagination.Next
	}
	return "", nil
}

// EnsureRecoveryFlow builds a recovery flow a link can walk somebody through.
//
// The shape is the smallest that does the job: a prompt for a new password and
// a stage that writes it. No identification stage, because a recovery link
// already says who it is for — it plants the person in the flow, which is what
// makes this work with no mail server and nothing to type.
func (c *Client) EnsureRecoveryFlow(ctx context.Context, appName string) (RecoveryFlow, error) {
	var out RecoveryFlow

	var repaired []string
	password, err := c.ensurePrompt(ctx, promptSpec{
		Name: appName + "-recovery-password", FieldKey: "password",
		Label: "New password", Type: "password", Order: 0,
	}, &repaired)
	if err != nil {
		return out, err
	}
	repeat, err := c.ensurePrompt(ctx, promptSpec{
		Name: appName + "-recovery-password-repeat", FieldKey: "password_repeat",
		Label: "New password (again)", Type: "password", Order: 1,
	}, &repaired)
	if err != nil {
		return out, err
	}

	prompt, err := c.ensurePromptStage(ctx, appName+"-recovery-prompt", []string{password, repeat})
	if err != nil {
		return out, err
	}
	write, err := c.ensureUserWriteStage(ctx, appName+"-recovery-write", &repaired)
	if err != nil {
		return out, err
	}

	flow, created, err := c.ensureFlow(ctx, &repaired, flowSpec{
		Name:        appName + " recovery",
		Slug:        appName + "-recovery",
		Title:       "Set up your access",
		Designation: "recovery",
	})
	if err != nil {
		return out, err
	}
	out.PK, out.Slug, out.Created = flow, appName+"-recovery", created

	// Order leaves room between them, so an operator adding a stage of their
	// own — a second factor, a consent — has somewhere to put it without
	// renumbering ours.
	// And then sign them in **as themselves**, which is what makes the link an
	// onboarding rather than a password reset.
	//
	// Without this stage the flow writes the credential and stops, and the
	// browser keeps whatever authentik session it already had. On a first
	// installation that session is the person who ran the wizard — `akadmin`
	// — so the new admin follows their link, sets a password, goes to the
	// console, and is refused: they signed in as the bootstrap account, which
	// holds no role here. Nothing on screen connects the two, and the link
	// they were given is single-use and now spent.
	login, err := c.ensureUserLoginStage(ctx, appName+"-recovery-login")
	if err != nil {
		return out, err
	}

	stages := []struct {
		stage string
		order int
	}{{prompt, 10}, {write, 20}, {login, 30}}

	// And then a passkey, which is the point of the whole arrangement.
	//
	// Without this the flow sets a password and stops, which is what the first
	// live run did: a member of staff followed their link, typed a password
	// twice, and ended up with exactly the credential this project took
	// trouble to avoid. A password is still set first — it makes the account
	// usable if enrolment is skipped or the device cannot do it — but the
	// passkey is what they are meant to sign in with.
	//
	// The stage is **reused, not created**: enrolment is a shared object every
	// flow on the instance can bind, and authentik ships one. Making a second
	// would be this wizard inventing its own WebAuthn policy. If the instance
	// has none, the flow is still a working way in and says nothing it cannot
	// deliver.
	passkey, err := c.webAuthnStage(ctx)
	if err != nil {
		return out, err
	}
	if passkey != "" {
		// After the login rather than before it: enrolment then happens in a
		// real session belonging to the right person, and somebody whose
		// device cannot do it is still signed in as themselves.
		stages = append(stages, struct {
			stage string
			order int
		}{passkey, 40})
		out.Passkey = true
	} else {
		log.Warn().Msg(
			"authentik: no webauthn enrolment stage, so the recovery flow ends at a password")
	}

	for _, binding := range stages {
		if err := c.ensureBinding(ctx, flow, binding.stage, binding.order); err != nil {
			return out, err
		}
	}

	bound, err := c.bindRecoveryFlowToBrand(ctx, flow)
	if err != nil {
		return out, err
	}
	out.BoundToBrand = bound
	out.Repaired = repaired
	for _, change := range repaired {
		log.Warn().Msg("authentik: " + change)
	}
	return out, nil
}

type promptSpec struct {
	Name     string
	FieldKey string
	Label    string
	Type     string
	Order    int
}

// ensurePrompt finds one of this site's prompts by name, or makes it.
//
// By name rather than by field key: `password` is a field key half the prompts
// in a directory use, and adopting somebody else's is how a wizard quietly
// edits a flow it did not create.
func (c *Client) ensurePrompt(ctx context.Context, spec promptSpec, repaired *[]string) (string, error) {
	type prompt struct {
		PK       string `json:"pk"`
		Name     string `json:"name"`
		FieldKey string `json:"field_key"`
		Type     string `json:"type"`
		Required bool   `json:"required"`
	}
	found, ok, err := findItem(c, ctx, "/stages/prompt/prompts/", spec.Name, func(p prompt) string { return p.Name })
	if err != nil {
		return "", err
	}
	if ok {
		// A password prompt that became something else would set nothing,
		// and the flow would end with no way in.
		if found.FieldKey != spec.FieldKey || found.Type != spec.Type || !found.Required {
			body := map[string]any{"field_key": spec.FieldKey, "type": spec.Type, "required": true}
			if err := c.do(ctx, http.MethodPatch, "/stages/prompt/prompts/"+url.PathEscape(found.PK)+"/", body, nil); err != nil {
				return "", fmt.Errorf("bring the %q prompt in line: %w", spec.Name, err)
			}
			*repaired = append(*repaired, "the prompt "+spec.Name+" asks for a password again")
		}
		return found.PK, nil
	}

	var made prompt
	body := map[string]any{
		"name": spec.Name, "field_key": spec.FieldKey, "label": spec.Label,
		"type": spec.Type, "required": true, "order": spec.Order,
		"placeholder": spec.Label,
	}
	if err := c.do(ctx, http.MethodPost, "/stages/prompt/prompts/", body, &made); err != nil {
		return "", fmt.Errorf("create the %q prompt: %w", spec.Name, err)
	}
	return made.PK, nil
}

func (c *Client) ensurePromptStage(ctx context.Context, name string, fields []string) (string, error) {
	type stage struct {
		PK   string `json:"pk"`
		Name string `json:"name"`
	}
	if found, err := findByName[stage](c, ctx, "/stages/prompt/stages/", name,
		func(s stage) (string, string) { return s.PK, s.Name }); err != nil || found != "" {
		return found, err
	}

	var made stage
	body := map[string]any{"name": name, "fields": fields}
	if err := c.do(ctx, http.MethodPost, "/stages/prompt/stages/", body, &made); err != nil {
		return "", fmt.Errorf("create the %q prompt stage: %w", name, err)
	}
	return made.PK, nil
}

// ensureUserLoginStage signs somebody in at the end of their own recovery.
//
// The defaults are authentik's: this does not shorten a session, bind it to a
// network, or terminate anybody else's. It exists to replace the browser's
// session with the person the link was for, and nothing more.
func (c *Client) ensureUserLoginStage(ctx context.Context, name string) (string, error) {
	type stage struct {
		PK   string `json:"pk"`
		Name string `json:"name"`
	}
	if found, err := findByName[stage](c, ctx, "/stages/user_login/", name,
		func(s stage) (string, string) { return s.PK, s.Name }); err != nil || found != "" {
		return found, err
	}

	var made stage
	if err := c.do(ctx, http.MethodPost, "/stages/user_login/",
		map[string]any{"name": name}, &made); err != nil {
		return "", fmt.Errorf("create the %q login stage: %w", name, err)
	}
	return made.PK, nil
}

// ensureUserWriteStage writes the new password onto the person the link named.
//
// `never_create` deliberately: this stage exists to set a credential on
// somebody who already exists, and a recovery flow that could create accounts
// is a recovery flow that can be used to make one.
//
// Reconciled on every run, because it is the one setting here that is about
// safety rather than function: a recovery write stage somebody switched to
// creating accounts is a way to make one.
func (c *Client) ensureUserWriteStage(ctx context.Context, name string, repaired *[]string) (string, error) {
	type stage struct {
		PK           string `json:"pk"`
		Name         string `json:"name"`
		CreationMode string `json:"user_creation_mode"`
	}
	found, ok, err := findItem(c, ctx, "/stages/user_write/", name, func(s stage) string { return s.Name })
	if err != nil {
		return "", err
	}
	if ok {
		if found.CreationMode != "never_create" {
			body := map[string]any{"user_creation_mode": "never_create"}
			if err := c.do(ctx, http.MethodPatch, "/stages/user_write/"+url.PathEscape(found.PK)+"/", body, nil); err != nil {
				return "", fmt.Errorf("stop the %q stage creating accounts: %w", name, err)
			}
			*repaired = append(*repaired, "the recovery flow can no longer create accounts")
		}
		return found.PK, nil
	}

	var made stage
	body := map[string]any{"name": name, "user_creation_mode": "never_create"}
	if err := c.do(ctx, http.MethodPost, "/stages/user_write/", body, &made); err != nil {
		return "", fmt.Errorf("create the %q user write stage: %w", name, err)
	}
	return made.PK, nil
}

type flowSpec struct {
	Name        string
	Slug        string
	Title       string
	Designation string
}

func (c *Client) ensureFlow(ctx context.Context, repaired *[]string, spec flowSpec) (string, bool, error) {
	existing, err := c.Flow(ctx, spec.Slug)
	switch {
	case err == nil:
		// A flow that is no longer a recovery flow is not what the brand
		// can bind, and one that asks for a session cannot be reached from
		// a link by somebody who has none.
		if existing.Designation != spec.Designation || existing.Authentication != "none" {
			body := map[string]any{"designation": spec.Designation, "authentication": "none"}
			if err := c.do(ctx, http.MethodPatch, "/flows/instances/"+url.PathEscape(spec.Slug)+"/", body, nil); err != nil {
				return "", false, fmt.Errorf("bring the %q flow in line: %w", spec.Slug, err)
			}
			*repaired = append(*repaired, "the flow "+spec.Slug+" is a recovery flow reachable from a link again")
		}
		return existing.PK, false, nil
	case !errors.Is(err, ErrNotFound):
		return "", false, err
	}

	var made struct {
		PK string `json:"pk"`
	}
	body := map[string]any{
		"name": spec.Name, "slug": spec.Slug, "title": spec.Title,
		"designation": spec.Designation,
		// Reached by a link from a signed-out browser, which is the whole
		// point: requiring a session would mean somebody had to sign in before
		// they could be given a way to sign in.
		"authentication": "none",
	}
	if err := c.do(ctx, http.MethodPost, "/flows/instances/", body, &made); err != nil {
		return "", false, fmt.Errorf("create the %q flow: %w", spec.Slug, err)
	}
	log.Warn().Str("flow", spec.Slug).Msg("authentik: created a recovery flow for this site")
	return made.PK, true, nil
}

// ensureBinding puts a stage in a flow, once.
func (c *Client) ensureBinding(ctx context.Context, flowPK, stagePK string, order int) error {
	var found page[struct {
		PK    string `json:"pk"`
		Stage string `json:"stage"`
	}]
	path := "/flows/bindings/?target=" + url.QueryEscape(flowPK) + "&page_size=100"
	if err := c.do(ctx, http.MethodGet, path, nil, &found); err != nil {
		return fmt.Errorf("read the flow's stages: %w", err)
	}
	for _, binding := range found.Results {
		if binding.Stage == stagePK {
			return nil
		}
	}

	body := map[string]any{"target": flowPK, "stage": stagePK, "order": order}
	if err := c.do(ctx, http.MethodPost, "/flows/bindings/", body, nil); err != nil {
		return fmt.Errorf("put a stage in the %q flow: %w", flowPK, err)
	}
	return nil
}

// bindRecoveryFlowToBrand points the default brand at a recovery flow, and
// only if it has none.
//
// This is the one change here that everybody on the instance sees: it is what
// puts a recovery path on the brand's own sign-in. A brand that already names
// one carries a decision somebody made, and overwriting it would be this
// console reaching into how an operator's other applications recover — the
// thing the no-flows rule existed to prevent, and the part of it that stands.
func (c *Client) bindRecoveryFlowToBrand(ctx context.Context, flowPK string) (bool, error) {
	var found page[struct {
		UUID         string `json:"brand_uuid"`
		RecoveryFlow string `json:"flow_recovery"`
	}]
	if err := c.do(ctx, http.MethodGet, "/core/brands/?default=true", nil, &found); err != nil {
		return false, fmt.Errorf("read the default brand: %w", err)
	}
	if len(found.Results) == 0 {
		log.Warn().Msg("authentik: no default brand, so the recovery flow is not reachable yet")
		return false, nil
	}

	brand := found.Results[0]
	if brand.RecoveryFlow != "" {
		log.Info().Msg("authentik: the default brand already has a recovery flow; leaving it alone")
		return false, nil
	}

	body := map[string]any{"flow_recovery": flowPK}
	path := "/core/brands/" + url.PathEscape(brand.UUID) + "/"
	if err := c.do(ctx, http.MethodPatch, path, body, nil); err != nil {
		return false, fmt.Errorf("point the default brand at the recovery flow: %w", err)
	}
	log.Warn().Msg("authentik: the default brand now uses this site's recovery flow")
	return true, nil
}

// findItem walks a listing for one of this site's own objects and returns it
// whole, so what it is can be compared with what it should be. Paged to the
// end for the reason findByName gives.
func findItem[T any](c *Client, ctx context.Context, path, name string, nameOf func(T) string) (T, bool, error) {
	var zero T
	for next := 1; next > 0; {
		var listing page[T]
		paged := fmt.Sprintf("%s?page=%d&page_size=100", path, next)
		if err := c.do(ctx, http.MethodGet, paged, nil, &listing); err != nil {
			return zero, false, fmt.Errorf("list %s: %w", path, err)
		}
		for _, item := range listing.Results {
			if nameOf(item) == name {
				return item, true, nil
			}
		}
		next = listing.Pagination.Next
	}
	return zero, false, nil
}

// findByName walks a listing for one of this site's own objects.
//
// These endpoints have no name filter, so the match is made here. Paged to the
// end rather than taking the first page: stopping early would find nothing and
// create a second object with the same name, which is how a directory fills up
// with duplicates nobody can tell apart.
func findByName[T any](c *Client, ctx context.Context, path, name string,
	identify func(T) (pk string, found string)) (string, error) {
	for next := 1; next > 0; {
		var listing page[T]
		paged := fmt.Sprintf("%s?page=%d&page_size=100", path, next)
		if err := c.do(ctx, http.MethodGet, paged, nil, &listing); err != nil {
			return "", fmt.Errorf("list %s: %w", path, err)
		}
		for _, item := range listing.Results {
			pk, itemName := identify(item)
			if itemName == name {
				return pk, nil
			}
		}
		next = listing.Pagination.Next
	}
	return "", nil
}

// ConsoleToken is the credential this console keeps, as opposed to the one an
// operator pasted into the wizard.
type ConsoleToken struct {
	// Identifier is what the token is called in the directory, so an operator
	// can find it, read it and revoke it.
	Identifier string

	// Key is the secret. It goes straight to the backend to be sealed and
	// never near a template.
	Key string

	// Created says this run made it, rather than reading back the one a
	// previous run made.
	Created bool

	// Repaired lists what a re-run had to bring back in line.
	Repaired []string
}

// EnsureConsoleToken gives the console a credential of its own.
//
// # Why the pasted token is the wrong thing to keep
//
// The token typed into the wizard is a **bootstrap** credential: a person made
// it by hand so that a first run could create a provider it could not yet
// authenticate against. authentik defaults it to thirty minutes precisely
// because that is what such a token is for.
//
// Keeping it as the console's permanent credential conflates two different
// things, and the live installation showed exactly what that costs: setup
// worked, the first admin signed in, and half an hour later every sign-in and
// every curation decision failed at once, because a setup token had been
// quietly promoted to a service credential. Telling operators to untick
// "expiring" treats the symptom — it asks a person to defeat a default that is
// right for what they were actually making.
//
// So the wizard spends the pasted token once, on provisioning, and mints a
// **non-expiring token of its own** to keep. The pasted one is then free to
// expire, which is what it was built to do.
//
// # Whose rights it carries, and the better shape that is not built
//
// It belongs to the person who ran the wizard, so it carries exactly the
// rights they just used — no new privilege appears in the directory, and
// nothing is granted that provisioning did not already require.
//
// A **service account** would be better: the console would not depend on one
// human, and an admin who leaves could be deleted without taking the site's
// administration with them. It is not built because authentik grants
// permissions to *roles*, not users — so it would mean creating a role, a
// group, a set of permissions and the bindings between them, or else putting a
// service account in a superuser group, which is a far larger privilege than
// reusing the rights of the person already doing this. That is the right next
// step and it needs its own thought.
//
// # It is find-or-read, not find-or-create
//
// A second run reads the existing key back through `view_key` rather than
// making another token under a new name. A wizard that minted a credential per
// run would leave a directory full of live tokens nobody can tell apart, which
// is the opposite of revocable.
func (c *Client) EnsureConsoleToken(ctx context.Context, appName string, userPK int) (ConsoleToken, error) {
	return c.ensureAPIToken(ctx, appName+"-console", userPK,
		"Used by the schmerz-reformen backend to read editors' groups and manage them. "+
			"Revoking it stops every sign-in to the console.")
}

// ensureAPIToken finds a non-expiring API token by identifier, or mints one
// for a user, and reads its key.
func (c *Client) ensureAPIToken(ctx context.Context, identifier string, userPK int, description string) (ConsoleToken, error) {
	out := ConsoleToken{Identifier: identifier}

	var found page[struct {
		Identifier string `json:"identifier"`
		Intent     string `json:"intent"`
		Expiring   bool   `json:"expiring"`
	}]
	path := "/core/tokens/?identifier=" + url.QueryEscape(identifier) + "&page_size=1"
	if err := c.do(ctx, http.MethodGet, path, nil, &found); err != nil {
		return out, fmt.Errorf("look for this console's own token: %w", err)
	}

	if len(found.Results) > 0 {
		// Somebody may have made it expire, which is the thirty-minute
		// failure this token exists to prevent, back again.
		if existing := found.Results[0]; existing.Expiring || existing.Intent != "api" {
			body := map[string]any{"expiring": false, "intent": "api"}
			if err := c.do(ctx, http.MethodPatch, "/core/tokens/"+url.PathEscape(identifier)+"/", body, nil); err != nil {
				return out, fmt.Errorf("stop this site's own token expiring: %w", err)
			}
			out.Repaired = append(out.Repaired, "the token "+identifier+" no longer expires")
			log.Warn().Str("identifier", identifier).Msg("authentik: this site's own token no longer expires")
		}
		key, err := c.tokenKey(ctx, identifier)
		if err != nil {
			return out, err
		}
		out.Key = key
		return out, nil
	}

	body := map[string]any{
		"identifier": identifier,
		"intent":     "api",
		"user":       userPK,
		// The whole point. A console credential that expires is a console that
		// stops, and nothing about the stopping says why.
		"expiring":    false,
		"description": description,
	}
	if err := c.do(ctx, http.MethodPost, "/core/tokens/", body, nil); err != nil {
		return out, fmt.Errorf("create this console's own token: %w", err)
	}

	// The create does not answer with the key: it is read back deliberately,
	// through the endpoint that exists for showing it.
	key, err := c.tokenKey(ctx, identifier)
	if err != nil {
		return out, err
	}
	out.Key, out.Created = key, true

	log.Warn().Str("identifier", identifier).Msg(
		"authentik: minted this console's own non-expiring token; the setup token is no longer needed")
	return out, nil
}

func (c *Client) tokenKey(ctx context.Context, identifier string) (string, error) {
	var view struct {
		Key string `json:"key"`
	}
	path := "/core/tokens/" + url.PathEscape(identifier) + "/view_key/"
	if err := c.do(ctx, http.MethodGet, path, nil, &view); err != nil {
		return "", fmt.Errorf("read the key of token %q: %w", identifier, err)
	}
	if view.Key == "" {
		return "", fmt.Errorf("authentik returned no key for token %q", identifier)
	}
	return view.Key, nil
}
