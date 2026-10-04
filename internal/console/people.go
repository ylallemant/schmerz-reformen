package console

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/ylallemant/schmerz-reformen/internal/apiclient"
)

// The people page: who may use the console, and what each may do —
// administer, and edit for which collectives. Administrators only; the
// backend reads every answer from the directory and makes every change there.

func (c *console) registerPeopleRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/people", c.localized(c.people))
	mux.Handle("POST /settings/people", c.localized(c.savePeople))
}

// peoplePage lists the staff.
type peoplePage struct {
	page
	People apiclient.People

	// WayIn is a single-use link, shown once, straight after it was minted.
	// Rendered into the answer to the form rather than carried in a redirect:
	// a link in an address is in the browser's history and every log between
	// here and there, and this one is somebody's account.
	WayIn *apiclient.WayIn

	// Draft is what was typed into the add form, put back after a refusal.
	Draft personDraft
}

type personDraft struct {
	Username string
	Name     string
	Admin    bool
}

func (c *console) people(w http.ResponseWriter, r *http.Request) {
	c.renderPeople(w, r, http.StatusOK, nil)
}

func (c *console) renderPeople(w http.ResponseWriter, r *http.Request, status int, adjust func(*peoplePage)) {
	data := peoplePage{page: c.newPage(r, "people.title")}
	if !data.Admin {
		refused := c.newPage(r, "error.forbidden_title")
		refused.Problem = refused.T("error.forbidden")
		c.renderer.Render(w, http.StatusForbidden, "problem", refused)
		return
	}

	listing, err := c.staff(r).People(r.Context())
	if err != nil && apiclient.StatusOf(err) == 0 {
		// The backend itself is away: nothing on this page can be drawn.
		c.fail(w, r, err)
		return
	}
	data.People = listing
	if err != nil {
		// Anything the backend answered is said above an empty page: most
		// often "no identity provider is configured", or the directory being
		// down, which an administrator should read here.
		data.Problem = c.peopleProblem(data.page, err)
	}
	if adjust != nil {
		adjust(&data)
	}
	c.renderer.Render(w, status, "people", data)
}

func (c *console) savePeople(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	client := c.staff(r)
	pk, _ := strconv.Atoi(r.FormValue("pk"))
	username := strings.TrimSpace(r.FormValue("username"))

	refused := func(err error, draft *personDraft) {
		c.renderPeople(w, r, http.StatusUnprocessableEntity, func(data *peoplePage) {
			data.Problem = c.problemOf(data.page, err)
			if draft != nil {
				data.Draft = *draft
			}
		})
	}

	switch r.FormValue("action") {
	case "invite":
		admin := r.FormValue("admin") != ""
		name := strings.TrimSpace(r.FormValue("name"))
		link, err := client.InvitePerson(r.Context(), username, name, admin)
		if err != nil {
			refused(err, &personDraft{Username: username, Name: name, Admin: admin})
			return
		}
		c.renderPeople(w, r, http.StatusOK, func(data *peoplePage) { data.WayIn = &link })

	case "relink":
		link, err := client.RelinkPerson(r.Context(), pk, username)
		if err != nil {
			refused(err, nil)
			return
		}
		c.renderPeople(w, r, http.StatusOK, func(data *peoplePage) { data.WayIn = &link })

	case "roles":
		if err := client.SetPersonAdmin(r.Context(), pk, username, r.FormValue("admin") != ""); err != nil {
			refused(err, nil)
			return
		}
		redirect(w, r, "/settings/people", "saved")

	case "remove":
		if err := client.RemovePerson(r.Context(), pk, username); err != nil {
			refused(err, nil)
			return
		}
		redirect(w, r, "/settings/people", "removed")

	default:
		http.Error(w, "nothing to do", http.StatusBadRequest)
	}
}
