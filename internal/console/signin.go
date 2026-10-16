package console

import (
	"net/http"
	"time"

	"github.com/ylallemant/schmerz-reformen/internal/apiclient"
)

// The sign-in page: what setup stored beside what this installation uses,
// for the site's administrators. It can bring the stored values in line with
// what the deployment gives and nothing more — pointing the site at another
// directory stays the setup wizard's, behind a restart with --superuser. See
// the backend's signin.go.

func (c *console) registerSignInRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/sign-in", c.localized(c.signInPage))
	mux.Handle("POST /settings/sign-in", c.localized(c.alignSignIn))
}

type signInPage struct {
	page
	Status apiclient.SignInStatus

	// ConsoleIssuer and ConsoleClient are what this console signs people in
	// with when the deployment platform gave it its client; empty when it
	// uses the one setup made.
	ConsoleIssuer string
	ConsoleClient string

	// ProvisionedAt is when setup ran, zero when the backend does not say.
	ProvisionedAt time.Time
}

func (c *console) signInPage(w http.ResponseWriter, r *http.Request) {
	c.renderSignIn(w, r, http.StatusOK, nil)
}

func (c *console) renderSignIn(w http.ResponseWriter, r *http.Request, status int, adjust func(*signInPage)) {
	data := signInPage{page: c.newPage(r, "signin.title")}
	if !data.Admin {
		c.renderForbidden(w, r)
		return
	}
	if c.platform != nil {
		data.ConsoleIssuer, data.ConsoleClient = c.platform.Issuer, c.platform.ClientID
	}
	current, err := c.staff(r).SignInStatus(r.Context(), c.platform != nil)
	if err != nil {
		c.fail(w, r, err)
		return
	}
	data.Status = current
	if current.ProvisionedAt != nil {
		data.ProvisionedAt = *current.ProvisionedAt
	}
	if adjust != nil {
		adjust(&data)
	}
	c.renderer.Render(w, status, "signin", data)
}

func (c *console) alignSignIn(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	if _, err := c.staff(r).AlignSignIn(r.Context(), c.platform != nil); err != nil {
		if apiclient.StatusOf(err) == 0 || apiclient.StatusOf(err) == http.StatusForbidden {
			c.fail(w, r, err)
			return
		}
		c.renderSignIn(w, r, http.StatusUnprocessableEntity, func(data *signInPage) {
			data.Problem = c.problemOf(data.page, err)
		})
		return
	}
	redirect(w, r, "/settings/sign-in", "aligned")
}
