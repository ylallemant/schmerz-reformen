package console

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/rs/zerolog/log"
	"golang.org/x/oauth2"

	"github.com/ylallemant/schmerz-reformen/internal/apiclient"
	"github.com/ylallemant/schmerz-reformen/internal/staffauth"
)

// signIn is the console's side of OpenID Connect.
//
// Built from what the setup wizard provisioned, which the backend keeps: the
// issuer of this site's Authentik application, its client identifier, and the
// client secret — the one credential the console holds, because exchanging an
// authorisation code is its job. What an editor may do is not read from the
// token: the backend asks the directory.
type signIn struct {
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
}

// discoveryTimeout bounds asking the provider what it is.
const discoveryTimeout = 15 * time.Second

// newSignIn discovers the provisioned application and builds the client.
func newSignIn(ctx context.Context, settings apiclient.AuthSettings, clientSecret string) (*signIn, error) {
	ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()

	provider, err := oidc.NewProvider(ctx, settings.Issuer)
	if err != nil {
		return nil, fmt.Errorf("discover the identity provider at %s: %w", settings.Issuer, err)
	}

	return &signIn{
		oauth: oauth2.Config{
			ClientID:     settings.ClientID,
			ClientSecret: clientSecret,
			Endpoint:     provider.Endpoint(),
			// The address the wizard registered, which Authentik matches
			// strictly: stored once, used every time, so the two cannot drift.
			RedirectURL: strings.TrimRight(settings.ConsoleURL, "/") + CallbackPath,
			// `profile` carries the name and the username. No `email`: the
			// console has no use for an editor's address.
			Scopes: []string{oidc.ScopeOpenID, "profile"},
		},
		verifier: provider.Verifier(&oidc.Config{ClientID: settings.ClientID}),
	}, nil
}

// CallbackPath is where the provider sends an editor back to: the one the
// backend registered on the provider. See staffauth.CallbackPath.
const CallbackPath = staffauth.CallbackPath

// currentSignIn and useSignIn guard the client, which appears when the setup
// wizard finishes — while requests are being served.
func (c *console) currentSignIn() *signIn {
	c.signInMu.RLock()
	defer c.signInMu.RUnlock()
	return c.signIn
}

func (c *console) useSignIn(s *signIn) {
	c.signInMu.Lock()
	c.signIn = s
	c.signInMu.Unlock()
}

// connectIdentityProvider reads what the wizard stored and builds the sign-in
// from it.
//
// It may fail without stopping the console: a console that cannot reach its
// directory should still start, say so, and serve its setup wizard if that is
// what is wanted. And failing leaves it locked, not open — requireSignIn
// refuses everybody while there is no sign-in.
func (c *console) connectIdentityProvider(ctx context.Context) error {
	backend := c.backend.AsConsole(c.staffToken)
	settings, err := backend.AuthSettings(ctx)
	if err != nil {
		return fmt.Errorf("read the sign-in configuration: %w", err)
	}
	if !settings.Provisioned {
		log.Warn().Msg("no identity provider is configured: nobody can sign in to this console yet")
		return nil
	}
	if settings.Issuer == "" {
		return errors.New("the identity provider is configured and its settings cannot be read — see the backend's log")
	}
	secret, err := backend.ClientSecret(ctx)
	if err != nil {
		return fmt.Errorf("read the sign-in credentials: %w", err)
	}

	client, err := newSignIn(ctx, settings, secret)
	if err != nil {
		return err
	}
	c.useSignIn(client)
	if settings.AdminGroupName != "" {
		c.setAdminGroup(settings.AdminGroupName)
	}
	log.Info().Str("issuer", settings.Issuer).Str("redirect", client.oauth.RedirectURL).
		Msg("editors sign in through the identity provider")
	return nil
}

func (c *console) registerAuthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/login", c.login)
	mux.HandleFunc("GET "+CallbackPath, c.callback)
	// A form post rather than a link: signing out is a change, and a link
	// would be followed by every prefetcher that touches the page.
	mux.HandleFunc("POST /auth/logout", c.logout)
	mux.Handle("GET /auth/signed-out", c.localization.Middleware(http.HandlerFunc(c.signedOut)))
}

// login starts a sign-in: three fresh secrets into a short-lived cookie, and
// the editor's browser off to the provider.
func (c *console) login(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.URL.Query().Get("next"))

	if c.development {
		// Nothing to sign in to. In development everybody already is.
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	client := c.currentSignIn()
	if client == nil {
		c.renderLocked(w, r)
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
	target := client.oauth.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:])),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// callback finishes a sign-in.
func (c *console) callback(w http.ResponseWriter, r *http.Request) {
	client := c.currentSignIn()
	if client == nil {
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

	token, err := client.oauth.Exchange(r.Context(), r.URL.Query().Get("code"),
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
	verified, err := client.verifier.Verify(r.Context(), raw)
	if err != nil {
		log.Warn().Err(err).Msg("an identity token did not verify")
		c.refuseSignIn(w, r, "the identity provider's answer could not be verified")
		return
	}
	if verified.Nonce != started.Nonce {
		c.refuseSignIn(w, r, "the answer does not belong to the sign-in this browser started")
		return
	}

	identity, err := identityOfToken(verified)
	if err != nil {
		log.Warn().Err(err).Msg("cannot read an identity token's claims")
		c.refuseSignIn(w, r, "the identity provider did not say who signed in")
		return
	}

	// What they may do is the backend's answer, read from the directory —
	// asked now, so somebody with no role here is told so at the door rather
	// than shown an empty console.
	profile, err := c.profileOf(r.Context(), identity)
	if err != nil {
		log.Error().Err(err).Str("username", identity.Username).Msg("cannot read what a signing-in editor may do")
		c.renderSignInFailure(w, r, http.StatusServiceUnavailable, "auth.directory_down")
		return
	}
	if !profile.Allowed {
		// They authenticated perfectly well and hold no role here. Named,
		// because the commonest cause is being signed in to the identity
		// provider as somebody else — the bootstrap account that set this
		// site up, say — and "you hold no role" alone sends them looking for
		// a missing role rather than at the account they are using. It leaks
		// nothing: they have just proved they hold that account.
		log.Warn().Str("username", identity.Username).Msg("console sign-in refused: the account holds no role here")
		c.inLanguage(w, r, func(w http.ResponseWriter, r *http.Request) {
			data := signedOutPage{page: c.newPage(r, "auth.failed_title")}
			data.Refused = identity.Username
			if data.Refused == "" {
				data.Refused = identity.Name
			}
			c.renderer.Render(w, http.StatusForbidden, "signedout", data)
		})
		return
	}
	identity.Groups = profile.Groups

	if err := c.startSession(w, identity); err != nil {
		log.Error().Err(err).Msg("cannot start a console session")
		http.Error(w, "cannot sign in", http.StatusInternalServerError)
		return
	}

	// INFO: a sign-in is a slow-frequency change of state, and "who was in
	// the console on Tuesday" is a question worth being able to answer. The
	// groups are DEBUG — they are detail, and there can be many.
	log.Info().Str("subject", identity.Subject).Str("username", identity.Username).Msg("an editor signed in")
	log.Debug().Str("subject", identity.Subject).Strs("groups", identity.Groups).
		Msg("groups the directory reported")

	http.Redirect(w, r, safeNext(started.Next), http.StatusSeeOther)
}

// identityOfToken reads who signed in out of a verified token: who they are,
// and nothing about what they may do — that is the directory's answer.
func identityOfToken(token *oidc.IDToken) (staffauth.Identity, error) {
	var claims map[string]any
	if err := token.Claims(&claims); err != nil {
		return staffauth.Identity{}, err
	}

	identity := staffauth.Identity{Subject: token.Subject}
	if username, ok := claims["preferred_username"].(string); ok {
		identity.Username = strings.TrimSpace(username)
	}
	// A name to show, in the order a person would recognise themselves. Never
	// the email address: it is not asked for.
	for _, claim := range []string{"name", "preferred_username", "nickname"} {
		if value, ok := claims[claim].(string); ok && strings.TrimSpace(value) != "" {
			identity.Name = strings.TrimSpace(value)
			break
		}
	}
	if identity.Name == "" {
		identity.Name = token.Subject
	}
	return identity, nil
}

// profileOf asks the backend what somebody may do.
func (c *console) profileOf(ctx context.Context, identity staffauth.Identity) (apiclient.StaffProfile, error) {
	client, err := c.backend.AsStaff(c.staffToken, identity)
	if err != nil {
		return apiclient.StaffProfile{}, err
	}
	return client.StaffMe(ctx)
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

	// Refused names an account that signed in and holds no role here.
	Refused string

	// Reason is a catalogue key for a failure worth saying more about.
	Reason string
}

// renderSignInFailure says why a sign-in did not work, when it is something
// the person can act on or should report.
func (c *console) renderSignInFailure(w http.ResponseWriter, r *http.Request, status int, reason string) {
	c.inLanguage(w, r, func(w http.ResponseWriter, r *http.Request) {
		data := signedOutPage{page: c.newPage(r, "auth.failed_title")}
		data.Failed = true
		data.Reason = reason
		c.renderer.Render(w, status, "signedout", data)
	})
}

// inLanguage renders a page from outside the routes' own language middleware
// — the sign-in guard and the callback run before any page does — so it is
// still in the reader's language rather than in catalogue keys.
func (c *console) inLanguage(w http.ResponseWriter, r *http.Request, render http.HandlerFunc) {
	c.localization.Middleware(render).ServeHTTP(w, r)
}

// renderLocked is the whole console while no identity provider is
// configured: nobody can be authenticated, so nobody is let in. Named by port
// rather than address — an operator knows their own maintenance port, and the
// address would put it on a page anybody can reach.
func (c *console) renderLocked(w http.ResponseWriter, r *http.Request) {
	c.inLanguage(w, r, func(w http.ResponseWriter, r *http.Request) {
		data := signedOutPage{page: c.newPage(r, "auth.locked_title")}
		data.Reason = "auth.locked"
		c.renderer.Render(w, http.StatusServiceUnavailable, "signedout", data)
	})
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

		if !c.development && c.currentSignIn() == nil {
			// Locked by default: a console that cannot authenticate anybody
			// must not therefore let everybody in.
			c.renderLocked(w, r)
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

		if !c.development {
			identity = c.refreshed(w, r, identity)
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
