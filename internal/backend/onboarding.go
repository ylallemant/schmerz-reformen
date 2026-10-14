package backend

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/authentik"
	"github.com/ylallemant/schmerz-reformen/internal/models"
	"github.com/ylallemant/schmerz-reformen/internal/staffauth"
	"github.com/ylallemant/schmerz-reformen/internal/store"
)

// Somebody whose address is at one of the onboarding domains makes their own
// console account, and waits for a site administrator to let it in.
//
//	console /auth/join  →  POST /v1/console/onboarding   account in <app>-pending,
//	                                                     way in shown to them
//	People page         →  PUT  /v1/staff/people/{pk}/approval   into <app>-users
//	                    →  DELETE …/approval                     deactivated
//
// # Why the account waits
//
// Nothing is sent to the address — this project runs no mail server — so the
// address is a claim, and anybody could type somebody else's. The account
// they make can set up its passkey at once and do nothing else: the waiting
// group is not a role, and the console's door refuses it, saying it waits.
// An administrator who recognises the person lets them in; one who does not
// turns the account away, and it is deactivated rather than deleted — the
// backend's account in the directory may not delete anybody, and an operator
// can still look at what was made.
//
// # What is never done
//
// No way in is ever minted for an account that already exists, whether by
// username or by address. A link for an existing account would let whoever
// typed its address replace its credentials; somebody who already has an
// account signs in with it, or asks an administrator for a new way in.

const (
	// onboardingBurst and onboardingRefill bound how fast accounts can be
	// made from the open form, across everybody: the console reaches the
	// backend from one address whoever is typing, so the limit is the
	// directory's to protect rather than one caller's.
	onboardingBurst  = 5
	onboardingRefill = 30 * time.Second

	// maxWaiting is how many accounts may wait for approval at once. The
	// form is reachable by anybody; past this, a flood stops making accounts
	// in somebody else's directory until an administrator has looked.
	maxWaiting = 50
)

func (a *API) registerOnboardingRoutes(api huma.API) {
	huma.Register(api, consoleOnly(huma.Operation{
		OperationID: "console-onboard",
		Method:      http.MethodPost,
		Path:        "/v1/console/onboarding",
		Summary:     "Make your own console account, to wait for approval",
		Description: "Only for an address at one of the onboarding domains, and only for one no " +
			"account has yet. The account waits outside every role until a site administrator " +
			"approves it; the single-use way in is returned once.",
		Tags: []string{"People"},
	}), a.consoleOnboard)

	huma.Register(api, adminOnly(huma.Operation{
		OperationID: "staff-approve-person",
		Method:      http.MethodPut,
		Path:        "/v1/staff/people/{pk}/approval",
		Summary:     "Let in somebody who made their own account",
		Tags:        []string{"People"},
	}), a.staffApprovePerson)

	huma.Register(api, adminOnly(huma.Operation{
		OperationID: "staff-refuse-person",
		Method:      http.MethodDelete,
		Path:        "/v1/staff/people/{pk}/approval",
		Summary:     "Turn away somebody who made their own account",
		Description: "Takes the account out of the waiting group and deactivates it. It is not " +
			"deleted: an operator can look at it, and reactivate it, in the directory.",
		Tags: []string{"People"},
	}), a.staffRefusePerson)

	huma.Register(api, adminOnly(huma.Operation{
		OperationID: "staff-set-onboarding-domains",
		Method:      http.MethodPut,
		Path:        "/v1/staff/onboarding",
		Summary:     "Choose which domains may make their own account",
		Tags:        []string{"People"},
	}), a.staffSetOnboardingDomains)
}

// pendingGroup is the group self-made accounts wait in.
func (a *API) pendingGroup() string { return models.PendingGroupName(a.appName()) }

// OnboardInput is what the join form sends.
type OnboardInput struct {
	Body struct {
		Email string `json:"email" doc:"an address at one of the onboarding domains; it becomes the username"`
		Name  string `json:"name" doc:"how colleagues will recognise them"`
	}
}

func (a *API) consoleOnboard(ctx context.Context, in *OnboardInput) (*LinkOutput, error) {
	settings, err := a.store.AuthSettings(ctx)
	if err != nil {
		log.Error().Err(err).Msg("cannot read the sign-in settings for an onboarding")
		return nil, huma.Error500InternalServerError("cannot read the sign-in configuration")
	}
	if !settings.Provisioned {
		return nil, huma.Error503ServiceUnavailable("no identity provider is configured yet")
	}
	domains := settings.OnboardingDomainList()
	if len(domains) == 0 {
		return nil, huma.Error403Forbidden("nobody can make their own account here; ask an administrator to add you")
	}

	email := strings.ToLower(strings.TrimSpace(in.Body.Email))
	if email, err = cleanEmail("the email", email); err != nil {
		return nil, err
	}
	if email == "" {
		return nil, huma.Error422UnprocessableEntity("an email address is needed")
	}
	if !models.MayOnboard(email, domains) {
		return nil, huma.Error403Forbidden("addresses at that domain cannot make their own account; " +
			"ask an administrator to add you")
	}
	name, err := cleanLine("the name", in.Body.Name, maxNameRunes)
	if err != nil {
		return nil, err
	}
	if name == "" {
		return nil, huma.Error422UnprocessableEntity("a name is needed")
	}

	if a.onboardings != nil && !a.onboardings.Allow("onboarding") {
		return nil, huma.Error429TooManyRequests("too many accounts were made just now; try again in a minute")
	}
	client, err := a.peopleClient()
	if err != nil {
		return nil, err
	}

	// Never a way in for an account that exists: by its username, or by
	// the address on it — which is what somebody adopted from elsewhere in
	// the directory, or added by an administrator under another username,
	// would have.
	if _, err := client.UserByUsername(ctx, email); err == nil {
		return nil, accountExists()
	} else if !errors.Is(err, authentik.ErrNotFound) {
		return nil, directoryRefusal(err, "cannot check whether the account exists")
	}
	if existing, err := client.UsersByEmail(ctx, email); err != nil {
		return nil, directoryRefusal(err, "cannot check whether the account exists")
	} else if len(existing) > 0 {
		return nil, accountExists()
	}

	waiting, err := client.UsersInGroup(ctx, a.pendingGroup())
	if err != nil && !errors.Is(err, authentik.ErrNotFound) {
		return nil, directoryRefusal(err, "cannot count who is waiting")
	}
	if len(waiting) >= maxWaiting {
		log.Warn().Int("waiting", len(waiting)).Msg("an onboarding was refused: too many accounts are waiting for approval")
		return nil, huma.Error503ServiceUnavailable("too many people are waiting to be let in; " +
			"try again once an administrator has looked at them")
	}

	// Nothing is made until it can be handed over.
	if found, err := client.Check(ctx); err == nil && !found.HasRecoveryFlow {
		return nil, huma.Error503ServiceUnavailable("accounts cannot be handed over yet: " +
			"the identity provider has no recovery flow")
	}

	pending, err := client.EnsureGroup(ctx, a.pendingGroup())
	if err != nil {
		return nil, directoryRefusal(err, "cannot find the group "+a.pendingGroup())
	}
	created, err := client.CreateUser(ctx, authentik.UserSpec{
		Username: email, Name: name, Email: email, Groups: []string{pending.PK},
	})
	if err != nil {
		return nil, directoryRefusal(err, "cannot make the account")
	}
	link, err := client.RecoveryLink(ctx, created.PK)
	if err != nil {
		// The account exists and waits; an administrator can approve it and
		// mint a way in from the People page.
		return nil, directoryRefusal(err, "the account was made, and no way in could be; ask an administrator")
	}

	// About staff, so written to the audit log — with the person as their own
	// actor, since nobody signed in did this.
	self := &staff{Identity: staffauth.Identity{Subject: "self:" + email, Username: email, Name: name}}
	a.audit(ctx, self, models.AuditCreate, "person", email, "",
		name+" made their own account, waiting for approval")
	a.waiting.forget()
	log.Warn().Str("username", email).Msg("somebody made their own console account; it waits for approval")

	out := &LinkOutput{}
	out.Body.Username, out.Body.Link = email, link
	return out, nil
}

func accountExists() error {
	return huma.Error409Conflict("there is already an account with this address: sign in with it, " +
		"or ask an administrator for a new way in")
}

// ApprovalInput addresses somebody waiting.
type ApprovalInput struct {
	PK       int    `path:"pk"`
	Username string `query:"username" doc:"their username, so their roles are read afresh at once"`
}

func (a *API) staffApprovePerson(ctx context.Context, in *ApprovalInput) (*DoneRolesOutput, error) {
	who, client, err := a.waitingPerson(ctx, in.PK)
	if err != nil {
		return nil, err
	}
	if err := a.setMembership(ctx, client, a.usersGroup(), in.PK, true); err != nil {
		return nil, err
	}
	if err := a.setMembership(ctx, client, a.pendingGroup(), in.PK, false); err != nil {
		return nil, err
	}
	a.directory.forget(in.Username)
	a.waiting.forget()
	log.Warn().Str("by", who.Username).Str("username", in.Username).Msg("a self-made account was let in")
	return doneRoles(), nil
}

func (a *API) staffRefusePerson(ctx context.Context, in *ApprovalInput) (*DoneRolesOutput, error) {
	who, client, err := a.waitingPerson(ctx, in.PK)
	if err != nil {
		return nil, err
	}
	if err := a.setMembership(ctx, client, a.pendingGroup(), in.PK, false); err != nil {
		return nil, err
	}
	if err := client.Deactivate(ctx, in.PK); err != nil {
		return nil, directoryRefusal(err, "cannot deactivate the account")
	}
	a.directory.forget(in.Username)
	a.waiting.forget()
	log.Warn().Str("by", who.Username).Str("username", in.Username).Msg("a self-made account was turned away and deactivated")
	return doneRoles(), nil
}

// waitingPerson checks the request is an administrator's and that the person
// is waiting. Only them: approval would otherwise be a way to put anybody in
// the directory into the users' group, and refusal a way to deactivate them.
func (a *API) waitingPerson(ctx context.Context, pk int) (*staff, *authentik.Client, error) {
	who, err := mustAdmin(ctx)
	if err != nil {
		return nil, nil, err
	}
	client, err := a.peopleClient()
	if err != nil {
		return nil, nil, err
	}
	waiting, err := client.UsersInGroup(ctx, a.pendingGroup())
	if err != nil {
		return nil, nil, directoryRefusal(err, "cannot read who is waiting")
	}
	if !slices.ContainsFunc(waiting, func(person authentik.User) bool { return person.PK == pk }) {
		return nil, nil, huma.Error404NotFound("nobody with that key is waiting to be let in")
	}
	return who, client, nil
}

// OnboardingDomainsInput replaces the list.
type OnboardingDomainsInput struct {
	Body struct {
		Domains string `json:"domains" doc:"one per line, or separated by commas; empty switches onboarding off"`
	}
}

// OnboardingDomainsOutput is the list as stored.
type OnboardingDomainsOutput struct {
	Body struct {
		Domains []string `json:"domains"`
	}
}

func (a *API) staffSetOnboardingDomains(ctx context.Context, in *OnboardingDomainsInput) (*OnboardingDomainsOutput, error) {
	if _, err := mustAdmin(ctx); err != nil {
		return nil, err
	}
	domains, err := models.ParseDomains(in.Body.Domains)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity("each entry must be a domain, like sozialratschlag.de")
	}
	err = a.store.SetOnboardingDomains(ctx, domains)
	if errors.Is(err, store.ErrNotProvisioned) {
		return nil, huma.Error409Conflict("no identity provider is configured yet: run the console's setup first")
	}
	if err != nil {
		log.Error().Err(err).Msg("cannot store the onboarding domains")
		return nil, huma.Error500InternalServerError("cannot store the domains")
	}
	log.Info().Strs("domains", domains).Msg("the onboarding domains were changed")
	out := &OnboardingDomainsOutput{}
	out.Body.Domains = domains
	return out, nil
}

// waitingCount is how many accounts wait, for an administrator's home page.
//
// Kept for as long as a role is (roleCacheLifetime), so an administrator
// moving between pages does not cost the directory a request each time — the
// same bargain the roles make. Every change this site makes to who waits
// drops it; somebody approved directly in Authentik shows up within a minute.
type waitingCount struct {
	mu sync.Mutex
	n  int
	at time.Time
}

func (w *waitingCount) count(ctx context.Context, a *API) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.at.IsZero() && time.Since(w.at) < roleCacheLifetime {
		return w.n
	}
	client, _ := a.directory.connected()
	if client == nil {
		return 0
	}
	waiting, err := a.waitingPeople(ctx, client)
	if err != nil {
		// The number is a courtesy; a directory that cannot answer costs it,
		// not the page.
		log.Warn().Err(err).Msg("cannot count who waits to be let in")
		return 0
	}
	w.n, w.at = len(waiting), time.Now()
	return w.n
}

func (w *waitingCount) forget() {
	w.mu.Lock()
	w.at = time.Time{}
	w.mu.Unlock()
}

// PendingItem is somebody waiting to be let in.
type PendingItem struct {
	PK       int    `json:"pk"`
	Username string `json:"username"`
	Name     string `json:"name,omitempty"`
	Email    string `json:"email,omitempty"`
}

// waitingPeople lists who waits, by name. An absent group is nobody.
func (a *API) waitingPeople(ctx context.Context, client *authentik.Client) ([]PendingItem, error) {
	members, err := client.UsersInGroup(ctx, a.pendingGroup())
	if err != nil && !errors.Is(err, authentik.ErrNotFound) {
		return nil, err
	}
	waiting := []PendingItem{}
	for _, person := range members {
		if !person.IsActive {
			continue
		}
		waiting = append(waiting, PendingItem{PK: person.PK, Username: person.Username, Name: person.Name, Email: person.Email})
	}
	slices.SortFunc(waiting, func(x, y PendingItem) int { return strings.Compare(x.Name+x.Username, y.Name+y.Username) })
	return waiting, nil
}
