package console

import (
	"context"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/apiclient"
)

// Somebody at one of the onboarding domains makes their own account here,
// before they can sign in — so these pages are open, under /auth/ like the
// rest of the sign-in.
//
// The account the backend makes waits outside every role until a site
// administrator lets it in, because nothing proves the address typed belongs
// to whoever typed it. The way in is shown once, in the answer to the form —
// never in a redirect, where it would be in the browser's history and every
// log on the way. See the backend's onboarding.go.

func (c *console) registerJoinRoutes(mux *http.ServeMux) {
	mux.Handle("GET /auth/join", c.localization.Middleware(http.HandlerFunc(c.join)))
	mux.Handle("POST /auth/join", c.localization.Middleware(http.HandlerFunc(c.join)))
}

type joinPage struct {
	page

	// Domains are the ones that may join; none closes the page.
	Domains []string

	// Email and Name are what was typed, put back after a refusal.
	Email string
	Name  string

	// WayIn is the account's single-use link, shown once.
	WayIn *apiclient.WayIn
}

func (c *console) join(w http.ResponseWriter, r *http.Request) {
	if !c.development && c.currentSignIn() == nil {
		// Nobody can sign in yet, so an account would have nowhere to go.
		c.renderLocked(w, r)
		return
	}

	data := joinPage{page: c.newPage(r, "join.title")}
	settings, err := c.backend.AsConsole(c.staffToken).AuthSettings(r.Context())
	if err != nil {
		c.fail(w, r, err)
		return
	}
	data.Domains = settings.OnboardingDomains
	if len(data.Domains) == 0 {
		// Not offered here: the page says so rather than answering 404, so a
		// link somebody was sent explains itself.
		c.renderer.Render(w, http.StatusNotFound, "join", data)
		return
	}
	if r.Method == http.MethodGet {
		c.renderer.Render(w, http.StatusOK, "join", data)
		return
	}

	if !parseForm(w, r) {
		return
	}
	data.Email = strings.TrimSpace(r.FormValue("email"))
	data.Name = strings.TrimSpace(r.FormValue("name"))

	link, err := c.backend.AsConsole(c.staffToken).Onboard(r.Context(), data.Email, data.Name)
	if err != nil {
		if apiclient.StatusOf(err) == 0 {
			c.fail(w, r, err)
			return
		}
		data.Problem = c.problemOf(data.page, err)
		c.renderer.Render(w, http.StatusUnprocessableEntity, "join", data)
		return
	}

	// Shown once and kept nowhere: not for a cache, a proxy's or the
	// browser's.
	w.Header().Set("Cache-Control", "no-store")
	data.WayIn = &link
	log.Info().Str("username", link.Username).Msg("somebody made their own console account from the join page")
	c.renderer.Render(w, http.StatusOK, "join", data)
}

// joinDomains are the domains the sign-in pages offer to join with. Nothing
// is offered when the backend cannot say: those pages are drawn when
// something has already gone wrong, and must not fail over a link.
func (c *console) joinDomains(ctx context.Context) []string {
	if !c.development && c.currentSignIn() == nil {
		return nil
	}
	settings, err := c.backend.AsConsole(c.staffToken).AuthSettings(ctx)
	if err != nil {
		log.Debug().Err(err).Msg("cannot read the onboarding domains for a sign-in page")
		return nil
	}
	return settings.OnboardingDomains
}
