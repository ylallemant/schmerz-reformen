package console

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/rs/zerolog/log"
	"golang.org/x/oauth2"

	"github.com/ylallemant/schmerz-reformen/internal/config"
	"github.com/ylallemant/schmerz-reformen/internal/staffauth"
)

// signIn is the console's side of OpenID Connect.
//
// The provider is Authentik, and nothing here knows that: it is an issuer URL
// and a client, which is what makes the identity provider a deployment
// decision rather than a dependency.
type signIn struct {
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier

	// groupsClaim names the claim that lists an identity's groups.
	groupsClaim string
}

// discoveryTimeout bounds asking the provider what it is. It runs once, at
// startup, and a console that cannot reach its identity provider should say so
// and stop rather than start up unable to sign anybody in.
const discoveryTimeout = 15 * time.Second

// newSignIn discovers the provider and builds the client.
func newSignIn(ctx context.Context, cfg config.OIDC) (*signIn, error) {
	ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()

	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("discover the identity provider at %s: %w", cfg.Issuer, err)
	}

	return &signIn{
		oauth: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  cfg.RedirectURL,
			// `profile` is what carries the name and — in Authentik's default
			// mapping — the groups. No `email`: the console has no use for an
			// editor's address, so it does not ask for one.
			Scopes: []string{oidc.ScopeOpenID, "profile"},
		},
		verifier:    provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		groupsClaim: cfg.GroupsClaim,
	}, nil
}

func (c *console) registerAuthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/login", c.login)
	mux.HandleFunc("GET /auth/callback", c.callback)
	// A form post rather than a link: signing out is a change, and a link
	// would be followed by every prefetcher that touches the page.
	mux.HandleFunc("POST /auth/logout", c.logout)
	mux.Handle("GET /auth/signed-out", c.localization.Middleware(http.HandlerFunc(c.signedOut)))
}

// login starts a sign-in: three fresh secrets into a short-lived cookie, and
// the editor's browser off to the provider.
func (c *console) login(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.URL.Query().Get("next"))

	if c.development || c.signIn == nil {
		// Nothing to sign in to. In development everybody already is.
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}

	state, err1 := randomToken()
	nonce, err2 := randomToken()
	verifier, err3 := randomToken()
	if err1 != nil || err2 != nil || err3 != nil {
		log.Error().Msg("cannot generate the secrets for a sign-in")
		http.Error(w, "cannot start signing in", http.StatusInternalServerError)
		return
	}

	expires := time.Now().Add(flowLifetime)
	sealed, err := c.sealer.seal(flow{
		State: state, Nonce: nonce, Verifier: verifier, Next: next, Expires: expires,
	})
	if err != nil {
		log.Error().Err(err).Msg("cannot record a sign-in in progress")
		http.Error(w, "cannot start signing in", http.StatusInternalServerError)
		return
	}
	c.setCookie(w, flowCookie, sealed, expires)

	// PKCE on top of a client secret. The secret proves the console is the
	// console; the challenge proves the browser finishing this is the one
	// that started it, which a secret held on the server says nothing about.
	challenge := sha256.Sum256([]byte(verifier))
	target := c.signIn.oauth.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:])),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// callback finishes a sign-in.
func (c *console) callback(w http.ResponseWriter, r *http.Request) {
	if c.signIn == nil {
		http.NotFound(w, r)
		return
	}

	// Read and spent in one go: whatever happens next, this sign-in attempt
	// is over and its secrets are not left lying in the browser.
	cookie, err := r.Cookie(flowCookie)
	c.clearCookie(w, flowCookie)
	if err != nil {
		c.refuseSignIn(w, r, "no sign-in was in progress in this browser")
		return
	}
	var started flow
	if err := c.sealer.open(cookie.Value, &started); err != nil || time.Now().After(started.Expires) {
		c.refuseSignIn(w, r, "the sign-in in this browser was not valid or had expired")
		return
	}

	if problem := r.URL.Query().Get("error"); problem != "" {
		// The provider said no: a cancelled prompt, an account not allowed to
		// use this application. Its own description is for the log, not the
		// page — it is text from another system.
		log.Info().Str("error", problem).
			Str("description", r.URL.Query().Get("error_description")).
			Msg("the identity provider refused a sign-in")
		c.refuseSignIn(w, r, "the identity provider refused the sign-in")
		return
	}
	if r.URL.Query().Get("state") != started.State || started.State == "" {
		c.refuseSignIn(w, r, "the answer does not belong to the sign-in this browser started")
		return
	}

	token, err := c.signIn.oauth.Exchange(r.Context(), r.URL.Query().Get("code"),
		oauth2.SetAuthURLParam("code_verifier", started.Verifier))
	if err != nil {
		log.Warn().Err(err).Msg("cannot exchange an authorisation code")
		c.refuseSignIn(w, r, "the identity provider did not confirm the sign-in")
		return
	}

	raw, ok := token.Extra("id_token").(string)
	if !ok {
		log.Warn().Msg("the identity provider answered without an identity token")
		c.refuseSignIn(w, r, "the identity provider did not say who signed in")
		return
	}
	verified, err := c.signIn.verifier.Verify(r.Context(), raw)
	if err != nil {
		log.Warn().Err(err).Msg("an identity token did not verify")
		c.refuseSignIn(w, r, "the identity provider's answer could not be verified")
		return
	}
	if verified.Nonce != started.Nonce {
		c.refuseSignIn(w, r, "the answer does not belong to the sign-in this browser started")
		return
	}

	identity, err := c.signIn.identity(verified)
	if err != nil {
		log.Warn().Err(err).Msg("cannot read an identity token's claims")
		c.refuseSignIn(w, r, "the identity provider did not say who signed in")
		return
	}

	if err := c.startSession(w, identity); err != nil {
		log.Error().Err(err).Msg("cannot start a console session")
		http.Error(w, "cannot sign in", http.StatusInternalServerError)
		return
	}

	// INFO: a sign-in is a slow-frequency change of state, and "who was in
	// the console on Tuesday" is a question worth being able to answer. The
	// groups are DEBUG — they are detail, and there can be many.
	log.Info().Str("subject", identity.Subject).Msg("an editor signed in")
	log.Debug().Str("subject", identity.Subject).Strs("groups", identity.Groups).
		Msg("groups the identity provider reported")

	http.Redirect(w, r, safeNext(started.Next), http.StatusSeeOther)
}

// identity reads who signed in out of a verified token.
func (s *signIn) identity(token *oidc.IDToken) (staffauth.Identity, error) {
	var claims map[string]any
	if err := token.Claims(&claims); err != nil {
		return staffauth.Identity{}, err
	}

	identity := staffauth.Identity{Subject: token.Subject}
	// A name to show, in the order a person would recognise themselves. Never
	// the email address: it is not asked for, and where a provider sends it
	// anyway it is not kept.
	for _, claim := range []string{"name", "preferred_username", "nickname"} {
		if value, ok := claims[claim].(string); ok && strings.TrimSpace(value) != "" {
			identity.Name = strings.TrimSpace(value)
			break
		}
	}
	if identity.Name == "" {
		identity.Name = token.Subject
	}

	identity.Groups = groupsFrom(claims[s.groupsClaim])
	return identity, nil
}

// groupsFrom reads a groups claim, which providers send as a list of names
// and occasionally as one name on its own.
//
// Anything else is no groups at all — and no groups is the safe reading: an
// editor in no group manages nothing, where a claim misread as "everything"
// would be the opposite.
func groupsFrom(claim any) []string {
	var groups []string
	switch value := claim.(type) {
	case []any:
		for _, entry := range value {
			if name, ok := entry.(string); ok && strings.TrimSpace(name) != "" {
				groups = append(groups, strings.TrimSpace(name))
			}
		}
	case string:
		if name := strings.TrimSpace(value); name != "" {
			groups = append(groups, name)
		}
	}
	return groups
}

// refuseSignIn says a sign-in did not work, and offers to start again.
//
// The reason is logged in full and shown in general: the page is the same
// whichever check failed, because the person looking at it can do exactly one
// thing about any of them, which is try again.
func (c *console) refuseSignIn(w http.ResponseWriter, r *http.Request, reason string) {
	log.Info().Str("reason", reason).Msg("a console sign-in was refused")
	http.Redirect(w, r, "/auth/signed-out?failed=1", http.StatusSeeOther)
}

// logout ends the console's own session.
//
// It does not sign the editor out of the identity provider. That session is
// theirs and covers other applications; ending it because somebody closed
// this one would be this console deciding something that is not its to decide.
// The page they land on says so, because the consequence — the next sign-in
// goes straight through — otherwise looks like signing out did not work.
func (c *console) logout(w http.ResponseWriter, r *http.Request) {
	c.clearCookie(w, sessionCookie)
	http.Redirect(w, r, "/auth/signed-out", http.StatusSeeOther)
}

type signedOutPage struct {
	page
	Failed bool
}

func (c *console) signedOut(w http.ResponseWriter, r *http.Request) {
	data := signedOutPage{page: c.newPage(r, "auth.signed_out_title")}
	data.Failed = r.URL.Query().Get("failed") != ""
	c.renderer.Render(w, http.StatusOK, "signedout", data)
}

// requireSignIn guards every page of the console.
//
// Middleware over the whole application port, with the exceptions listed here
// rather than a check in each handler: the page somebody forgets to guard is
// the one that needed it. What is exempt is what must be reachable before
// anybody has signed in — the sign-in itself, and the stylesheet the
// signed-out page is drawn with.
func (c *console) requireSignIn(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isOpenPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		identity, ok := c.identityOf(r)
		if !ok {
			if r.Method == http.MethodGet {
				target := "/auth/login?next=" + queryEscape(r.URL.RequestURI())
				http.Redirect(w, r, target, http.StatusSeeOther)
				return
			}
			// A write with no session is refused rather than redirected: a
			// redirect would turn the POST into a GET of the sign-in page and
			// silently drop whatever the editor had typed.
			http.Error(w, "sign in first", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), identity)))
	})
}

// openPaths are reachable without signing in.
var openPaths = []string{"/auth/", "/static/", "/theme/tokens.css", "/theme/assets/", "/theme/files/", "/language"}

func isOpenPath(path string) bool {
	// The API documentation is the service describing itself, and describes
	// nothing a reader of the source cannot see.
	if path == "/docs" || strings.HasPrefix(path, "/openapi") || strings.HasPrefix(path, "/schemas/") {
		return true
	}
	for _, prefix := range openPaths {
		if path == prefix || strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

type identityKeyType struct{}

var identityKey identityKeyType

func withIdentity(ctx context.Context, identity staffauth.Identity) context.Context {
	return context.WithValue(ctx, identityKey, identity)
}

// identityFrom returns who is signed in on a request the middleware let
// through.
func identityFrom(ctx context.Context) staffauth.Identity {
	identity, _ := ctx.Value(identityKey).(staffauth.Identity)
	return identity
}
