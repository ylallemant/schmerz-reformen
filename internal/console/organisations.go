package console

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/apiclient"
)

// Organisations are shared by every collective that lists them. The site's
// administrators create and delete them; an organisation's own administrators
// edit it and choose its people. Everybody else reads.

func (c *console) registerOrganisationRoutes(mux *http.ServeMux) {
	mux.Handle("GET /organisations", c.localized(c.organisations))
	mux.Handle("GET /organisations/new", c.localized(c.newOrganisation))
	mux.Handle("POST /organisations", c.localized(c.createOrganisation))
	mux.Handle("GET /organisations/{id}", c.localized(c.organisation))
	mux.Handle("POST /organisations/{id}", c.localized(c.saveOrganisation))
	mux.Handle("POST /organisations/{id}/logo", c.localized(c.saveOrganisationLogo))
	mux.Handle("POST /organisations/{id}/delete", c.localized(c.deleteOrganisation))
	mux.Handle("POST /organisations/{id}/people", c.localized(c.saveOrganisationPeople))
}

// organisationsPage lists every organisation.
type organisationsPage struct {
	page
	Organisations []apiclient.Organisation
}

func (c *console) organisations(w http.ResponseWriter, r *http.Request) {
	organisations, err := c.staff(r).Organisations(r.Context())
	if err != nil {
		c.fail(w, r, err)
		return
	}
	data := organisationsPage{page: c.newPage(r, "organisations.title")}
	data.Organisations = organisations
	c.renderer.Render(w, http.StatusOK, "organisations", data)
}

// organisationPage creates an organisation, or edits one.
type organisationPage struct {
	page

	// Organisation is what is stored; Form is what the fields show. They
	// differ after a refused save, when the form keeps what was typed.
	Organisation apiclient.Organisation
	Form         apiclient.OrganisationFields

	// Parents are what the parent picker offers.
	Parents []apiclient.Organisation

	// People is who administers it and who belongs to it, and Users who can
	// be added — filled for its administrators only.
	People apiclient.EntryPeople
	Users  []apiclient.User

	// PeopleProblem says why People could not be read.
	PeopleProblem string

	Zoom        int
	New         bool
	MemberKinds []string
}

func organisationForm(organisation apiclient.Organisation) apiclient.OrganisationFields {
	return apiclient.OrganisationFields{
		Name:     organisation.Name,
		Kind:     organisation.Kind,
		Website:  organisation.Website,
		ParentID: organisation.ParentID,
		Place: apiclient.Place{
			Latitude: organisation.Latitude, Longitude: organisation.Longitude, Place: organisation.Place,
		},
	}
}

func organisationFieldsFrom(r *http.Request) apiclient.OrganisationFields {
	return apiclient.OrganisationFields{
		Name:     r.FormValue("name"),
		Kind:     r.FormValue("kind"),
		Website:  r.FormValue("website"),
		ParentID: strings.TrimSpace(r.FormValue("parent_id")),
		Place:    placeFrom(r),
	}
}

// organisationData is the page with what every version of it needs: the
// organisations a parent may be chosen from.
func (c *console) organisationData(r *http.Request, titleKey string) (organisationPage, error) {
	data := organisationPage{page: c.newPage(r, titleKey)}
	data.UsesMap = true
	data.MemberKinds = memberKinds
	parents, err := c.staff(r).Organisations(r.Context())
	data.Parents = parents
	return data, err
}

func (c *console) newOrganisation(w http.ResponseWriter, r *http.Request) {
	data, err := c.organisationData(r, "organisation.new_title")
	if err != nil {
		c.fail(w, r, err)
		return
	}
	if !data.Admin {
		c.renderForbidden(w, r)
		return
	}
	data.New = true
	data.Form.Kind = "other"
	c.renderer.Render(w, http.StatusOK, "organisation", data)
}

// renderForbidden says a page is not this editor's, before they fill it in.
// The backend refuses anyway; this is the kinder order to learn it in.
func (c *console) renderForbidden(w http.ResponseWriter, r *http.Request) {
	refused := c.newPage(r, "error.forbidden_title")
	refused.Problem = refused.T("error.forbidden")
	c.renderer.Render(w, http.StatusForbidden, "problem", refused)
}

func (c *console) createOrganisation(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	fields := organisationFieldsFrom(r)

	created, err := c.staff(r).CreateOrganisation(r.Context(), fields)
	if err != nil {
		if apiclient.StatusOf(err) == http.StatusForbidden {
			c.fail(w, r, err)
			return
		}
		data, loadErr := c.organisationData(r, "organisation.new_title")
		if loadErr != nil {
			c.fail(w, r, loadErr)
			return
		}
		data.New = true
		data.Form = fields
		data.Zoom = fields.Zoom
		data.Problem = c.problemOf(data.page, err)
		c.renderer.Render(w, http.StatusUnprocessableEntity, "organisation", data)
		return
	}
	redirect(w, r, "/organisations/"+created.ID, "created")
}

func (c *console) organisation(w http.ResponseWriter, r *http.Request) {
	c.renderOrganisation(w, r, http.StatusOK, nil)
}

// renderOrganisation draws an organisation's page, letting the caller adjust
// it — which is how a refused save puts back what was typed.
func (c *console) renderOrganisation(w http.ResponseWriter, r *http.Request, status int, adjust func(*organisationPage)) {
	client := c.staff(r)
	organisation, err := client.Organisation(r.Context(), r.PathValue("id"))
	if err != nil {
		c.fail(w, r, err)
		return
	}
	data, err := c.organisationData(r, "organisation.edit_title")
	if err != nil {
		c.fail(w, r, err)
		return
	}
	data.Organisation = organisation
	data.Form = organisationForm(organisation)
	if organisation.Administers {
		data.People, data.Users, data.PeopleProblem = c.entryPeople(r, data.page, func() (apiclient.EntryPeople, error) {
			return client.OrganisationPeople(r.Context(), organisation.ID)
		})
	}
	if adjust != nil {
		adjust(&data)
	}
	c.renderer.Render(w, status, "organisation", data)
}

func (c *console) saveOrganisation(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	id := r.PathValue("id")
	fields := organisationFieldsFrom(r)

	if _, err := c.staff(r).SaveOrganisation(r.Context(), id, fields); err != nil {
		if apiclient.IsNotFound(err) || apiclient.StatusOf(err) == http.StatusForbidden {
			c.fail(w, r, err)
			return
		}
		c.renderOrganisation(w, r, http.StatusUnprocessableEntity, func(data *organisationPage) {
			data.Form = fields
			data.Zoom = fields.Zoom
			data.Problem = c.problemOf(data.page, err)
		})
		return
	}
	redirect(w, r, "/organisations/"+id, "saved")
}

func (c *console) saveOrganisationLogo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// Read before anything else looks at the form: a multipart body can be
	// parsed once, and the "remove" button is a field in it.
	data, contentType, uploadErr := uploadFrom(w, r, "logo")

	var err error
	switch {
	case r.FormValue("remove") != "":
		err = c.staff(r).DeleteOrganisationLogo(r.Context(), id)
	case uploadErr != nil:
		c.renderOrganisation(w, r, http.StatusUnprocessableEntity, func(data *organisationPage) {
			data.Problem = data.T("error.upload")
		})
		return
	default:
		err = c.staff(r).UploadOrganisationLogo(r.Context(), id, contentType, data)
	}
	if err != nil {
		if apiclient.IsNotFound(err) || apiclient.StatusOf(err) == http.StatusForbidden {
			c.fail(w, r, err)
			return
		}
		c.renderOrganisation(w, r, http.StatusUnprocessableEntity, func(data *organisationPage) {
			data.Problem = c.problemOf(data.page, err)
		})
		return
	}
	redirect(w, r, "/organisations/"+id, "logo")
}

func (c *console) deleteOrganisation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := c.staff(r).DeleteOrganisation(r.Context(), id); err != nil {
		if apiclient.IsNotFound(err) || apiclient.StatusOf(err) == http.StatusForbidden {
			c.fail(w, r, err)
			return
		}
		c.renderOrganisation(w, r, http.StatusUnprocessableEntity, func(data *organisationPage) {
			data.Problem = c.problemOf(data.page, err)
		})
		return
	}
	redirect(w, r, "/organisations", "deleted")
}

func (c *console) saveOrganisationPeople(w http.ResponseWriter, r *http.Request) {
	c.saveEntryPeople(w, r, "organisations", "/organisations/"+r.PathValue("id"),
		func(w http.ResponseWriter, r *http.Request, problem string) {
			c.renderOrganisation(w, r, http.StatusUnprocessableEntity, func(data *organisationPage) {
				data.Problem = problem
			})
		})
}

// entryPeople reads who holds the roles on a collective or an organisation,
// and who could be given one.
//
// The people are in the directory and the rest of the page is in the
// database, and the first can be away while the second is not — before the
// setup wizard has run, or while Authentik is down. The page is still worth
// having then, so a failure here is said in the people section rather than in
// place of the page.
func (c *console) entryPeople(r *http.Request, p page,
	read func() (apiclient.EntryPeople, error)) (apiclient.EntryPeople, []apiclient.User, string) {
	people, err := read()
	if err != nil {
		return people, nil, c.peopleProblem(p, err)
	}
	users, err := c.staff(r).Users(r.Context())
	if err != nil {
		return people, nil, c.peopleProblem(p, err)
	}
	return people, users, ""
}

// peopleProblem is why the directory could not be read, in the backend's
// words when it gave some: they name what an operator has to do.
func (c *console) peopleProblem(p page, err error) string {
	log.Warn().Err(err).Msg("cannot read who holds the roles on a page")
	var refused *apiclient.Error
	if errors.As(err, &refused) && refused.Message != "" {
		return p.Tf("people.unavailable", "Reason", refused.Message)
	}
	return p.T("error.backend")
}

// saveEntryPeople gives or takes one role on a collective or an organisation,
// from the people section of its page: `role`, `pk`, `username`, and `give`
// or `take`.
func (c *console) saveEntryPeople(w http.ResponseWriter, r *http.Request, kind, back string,
	refused func(http.ResponseWriter, *http.Request, string)) {
	if !parseForm(w, r) {
		return
	}
	pk, _ := strconv.Atoi(r.FormValue("pk"))
	give := r.FormValue("action") == "give"
	err := c.staff(r).SetEntryRole(r.Context(), kind, r.PathValue("id"), r.FormValue("role"),
		pk, strings.TrimSpace(r.FormValue("username")), give)
	if err != nil {
		if apiclient.IsNotFound(err) {
			c.fail(w, r, err)
			return
		}
		refused(w, r, c.problemOf(c.newPage(r, ""), err))
		return
	}
	redirect(w, r, back+"#people", "saved")
}
