package console

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/staffauth"
	"github.com/ylallemant/schmerz-reformen/internal/web"
)

// Cookies the console sets.
const (
	// sessionCookie holds who is signed in.
	sessionCookie = "schmerz_console"

	// flowCookie holds a sign-in that has been started and not yet finished:
	// the state, the nonce and the PKCE verifier the callback has to match.
	flowCookie = "schmerz_console_flow"
)

// sessionLifetime is how long a sign-in lasts: a working day.
//
// It is **not** how long a change of groups takes to land. What an editor may
// do is decided by the backend, which asks the directory — at most a minute
// behind for a read, never behind for a write. The groups kept in the cookie
// only decide which menus are drawn, and are refreshed every few minutes (see
// groupsRefresh).
const sessionLifetime = 8 * time.Hour

// groupsRefresh is how often the console asks again what a signed-in editor
// may do, so its menus follow a change made in the directory.
const groupsRefresh = 5 * time.Minute

// flowLifetime is how long somebody has to finish signing in. It covers
// typing a password and finding a phone, and not much else.
const flowLifetime = 10 * time.Minute

// sealer signs what the console puts in a cookie.
//
// # Signed, not encrypted, and not stored
//
// The console holds no database, so a session cannot be a row somewhere: it is
// the cookie itself, and what makes it believable is the signature. Nothing in
// it is secret from its owner — it is their own name and their own groups —
// so signing is the whole requirement, and an encrypted cookie would only add
// a key to rotate.
//
// What the signature buys is that the browser cannot edit the list. Without it
// "which groups am I in" would be a question the answer to which is supplied
// by the person asking.
type sealer struct {
	key []byte
}

// newSealer builds a sealer from the configured secret, or from a random one
// when none was configured. It reports whether the key is its own invention,
// so the caller can say so: a random key signs everybody out on every restart
// and cannot work behind two replicas, each of which would refuse the other's
// cookies.
func newSealer(secret string) (*sealer, bool, error) {
	if strings.TrimSpace(secret) != "" {
		// Hashed so that a short or oddly shaped secret still makes a full-
		// length key. It does not make a weak secret strong.
		sum := sha256.Sum256([]byte(secret))
		return &sealer{key: sum[:]}, false, nil
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, false, err
	}
	return &sealer{key: key}, true, nil
}

// errBadSeal covers every way a cookie can fail to be ours: malformed, signed
// with another key, or simply edited. The caller does not need to know which,
// and saying which would tell somebody forging one how close they got.
var errBadSeal = errors.New("cookie is not one this console signed")

// seal renders a value and its signature.
func (s *sealer) seal(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return payload + "." + s.sign(payload), nil
}

// open verifies a sealed value and decodes it.
func (s *sealer) open(sealed string, out any) error {
	payload, signature, found := strings.Cut(sealed, ".")
	if !found {
		return errBadSeal
	}
	// Constant-time: the comparison is of a signature an attacker is trying to
	// guess, and how long it took to refuse one is not something to tell them.
	if !hmac.Equal([]byte(signature), []byte(s.sign(payload))) {
		return errBadSeal
	}

	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return errBadSeal
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return errBadSeal
	}
	return nil
}

func (s *sealer) sign(payload string) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(payload)) //nolint:errcheck // a hash never fails to write
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// signedIn is the content of the session cookie.
type signedIn struct {
	Identity staffauth.Identity `json:"identity"`
	Expires  time.Time          `json:"expires"`

	// CheckedAt is when the groups in Identity were last read from the
	// backend.
	CheckedAt time.Time `json:"checked_at"`
}

// flow is the content of the cookie that carries a sign-in from the moment it
// is started to the moment the provider sends the editor back.
type flow struct {
	// State ties the callback to this browser: a callback whose state is not
	// the one in this cookie was started somewhere else.
	State string `json:"state"`

	// Nonce ties the identity token to this sign-in, so a token issued for
	// another one cannot be replayed here.
	Nonce string `json:"nonce"`

	// Verifier is the PKCE secret. The provider was shown only its hash; the
	// exchange has to produce the value, which only this browser's cookie
	// holds.
	Verifier string `json:"verifier"`

	// Next is where the editor was going when they were sent to sign in.
	Next string `json:"next"`

	Expires time.Time `json:"expires"`
}

// setCookie writes one of the console's cookies.
//
// HttpOnly, because nothing on a page has any business reading either;
// SameSite=Lax rather than Strict, because the sign-in itself is a navigation
// back from the identity provider and Strict would drop the cookie on exactly
// that request. Lax still withholds it from a cross-site POST, which is the
// request that matters.
func (c *console) setCookie(w http.ResponseWriter, name, value string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   c.secureCookies,
		SameSite: http.SameSiteLaxMode,
	})
}

func (c *console) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   c.secureCookies,
		SameSite: http.SameSiteLaxMode,
	})
}

// startSession signs an editor in.
func (c *console) startSession(w http.ResponseWriter, identity staffauth.Identity) error {
	return c.writeSession(w, signedIn{
		Identity: identity, Expires: time.Now().Add(sessionLifetime), CheckedAt: time.Now(),
	})
}

func (c *console) writeSession(w http.ResponseWriter, session signedIn) error {
	sealed, err := c.sealer.seal(session)
	if err != nil {
		return err
	}
	c.setCookie(w, sessionCookie, sealed, session.Expires)
	return nil
}

// refreshed brings a session's groups up to date when they are older than
// groupsRefresh, rewriting the cookie. The expiry is kept: refreshing groups
// is not signing in again.
//
// A backend that cannot answer leaves the groups as they were — they decide
// only what is drawn, and the backend decides everything else on every
// request anyway.
func (c *console) refreshed(w http.ResponseWriter, r *http.Request, identity staffauth.Identity) staffauth.Identity {
	session, ok := c.sessionOf(r)
	if !ok || time.Since(session.CheckedAt) < groupsRefresh {
		return identity
	}
	profile, err := c.profileOf(r.Context(), identity)
	if err != nil {
		log.Debug().Err(err).Msg("cannot refresh an editor's groups; keeping the ones in the session")
		return identity
	}
	session.Identity.Groups = profile.Groups
	session.CheckedAt = time.Now()
	if err := c.writeSession(w, session); err != nil {
		log.Error().Err(err).Msg("cannot rewrite a console session")
	}
	return session.Identity
}

// identityOf returns who is signed in on a request.
//
// The expiry is checked from inside the signed payload rather than left to the
// cookie's own attribute: a browser is asked to forget an expired cookie and
// nothing makes it, and a session that lasted as long as somebody chose to
// keep replaying it would not be a session.
func (c *console) identityOf(r *http.Request) (staffauth.Identity, bool) {
	if c.development {
		return c.developmentIdentity, true
	}

	session, ok := c.sessionOf(r)
	return session.Identity, ok
}

// sessionOf reads the session cookie, refusing one that is not ours or has
// expired.
func (c *console) sessionOf(r *http.Request) (signedIn, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return signedIn{}, false
	}
	var session signedIn
	if err := c.sealer.open(cookie.Value, &session); err != nil {
		return signedIn{}, false
	}
	if time.Now().After(session.Expires) || session.Identity.Subject == "" {
		return signedIn{}, false
	}
	return session, true
}

// randomToken returns an unguessable value for a state, a nonce or a verifier.
func randomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// safeNext keeps a post-sign-in destination on this site — see web.SafeNext —
// and out of the sign-in itself: being sent back into /auth/ after signing in
// is a loop at best.
func safeNext(next string) string {
	next = web.SafeNext(next)
	if next == "" || strings.HasPrefix(next, "/auth/") {
		return "/"
	}
	return next
}
