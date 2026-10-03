package console

import (
	"net/http"
	"strings"

	"github.com/ylallemant/schmerz-reformen/internal/apiclient"
)

// Organisations are shared by every collective that lists them, so a form here
// never changes one: it proposes a change, which the backend carries out once
// enough editors other than its author approve it. Every page says so, because
// "saved" would be a lie the editor discovers by looking.

func (c *console) registerOrganisationRoutes(mux *http.ServeMux) {
	mux.Handle("GET /organisations", c.localized(c.organisations))
	mux.Handle("GET /organisations/new", c.localized(c.newOrganisation))
	mux.Handle("POST /organisations", c.localized(c.proposeOrganisation))
	mux.Handle("GET /organisations/{id}", c.localized(c.organisation))
	mux.Handle("POST /organisations/{id}", c.localized(c.proposeOrganisationUpdate))
	mux.Handle("POST /organisations/{id}/logo", c.localized(c.proposeOrganisationLogo))
	mux.Handle("POST /organisations/{id}/delete", c.localized(c.proposeOrganisationDeletion))

	mux.Handle("GET /changes/{id}", c.localized(c.change))
	mux.Handle("POST /changes/{id}/vote", c.localized(c.vote))
	mux.Handle("POST /changes/{id}/withdraw", c.localized(c.withdraw))
}

// organisationsPage lists every organisation, and the changes to them.
type organisationsPage struct {
	page
	Organisations []apiclient.Organisation
	Pending       []apiclient.Change
	Decided       []apiclient.Change
}

func (c *console) organisations(w http.ResponseWriter, r *http.Request) {
	client := c.staff(r)
	organisations, err := client.Organisations(r.Context())
	if err != nil {
		c.fail(w, r, err)
		return
	}
	pending, _, err := client.Changes(r.Context(), "pending", "", 0)
	if err != nil {
		c.fail(w, r, err)
		return
	}
	decided, _, err := client.Changes(r.Context(), "decided", "", 10)
	if err != nil {
		c.fail(w, r, err)
		return
	}

	data := organisationsPage{page: c.newPage(r, "organisations.title")}
	data.Organisations = organisations
	data.Pending = pending
	data.Decided = decided
	c.renderer.Render(w, http.StatusOK, "organisations", data)
}

// organisationPage proposes a new organisation, or a change to one.
type organisationPage struct {
	page

	// Organisation is what is stored; Form is what the fields show. They
	// differ after a refused proposal, when the form keeps what was typed.
	Organisation apiclient.Organisation
	Form         apiclient.OrganisationFields

	// Parents are what the parent picker offers.
	Parents []apiclient.Organisation

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
	data.New = true
	data.Form.Kind = "other"
	c.renderer.Render(w, http.StatusOK, "organisation", data)
}

func (c *console) proposeOrganisation(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	fields := organisationFieldsFrom(r)

	change, err := c.staff(r).ProposeOrganisation(r.Context(), fields)
	if err != nil {
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
	redirect(w, r, "/changes/"+change.ID, "proposed")
}

func (c *console) organisation(w http.ResponseWriter, r *http.Request) {
	c.renderOrganisation(w, r, http.StatusOK, nil)
}

// renderOrganisation draws an organisation's page, letting the caller adjust
// it — which is how a refused proposal puts back what was typed.
func (c *console) renderOrganisation(w http.ResponseWriter, r *http.Request, status int, adjust func(*organisationPage)) {
	organisation, err := c.staff(r).Organisation(r.Context(), r.PathValue("id"))
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
	if adjust != nil {
		adjust(&data)
	}
	c.renderer.Render(w, status, "organisation", data)
}

func (c *console) proposeOrganisationUpdate(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	fields := organisationFieldsFrom(r)

	change, err := c.staff(r).ProposeOrganisationUpdate(r.Context(), r.PathValue("id"), fields)
	if err != nil {
		if apiclient.IsNotFound(err) {
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
	redirect(w, r, "/changes/"+change.ID, "proposed")
}

func (c *console) proposeOrganisationLogo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// Read before anything else looks at the form: a multipart body can be
	// parsed once, and the "remove" button is a field in it.
	data, contentType, uploadErr := uploadFrom(w, r, "logo")

	var err error
	switch {
	case r.FormValue("remove") != "":
		err = c.staff(r).ProposeOrganisationLogoRemoval(r.Context(), id)
	case uploadErr != nil:
		c.renderOrganisation(w, r, http.StatusUnprocessableEntity, func(data *organisationPage) {
			data.Problem = data.T("error.upload")
		})
		return
	default:
		err = c.staff(r).ProposeOrganisationLogo(r.Context(), id, contentType, data)
	}

	if err != nil {
		if apiclient.IsNotFound(err) {
			c.fail(w, r, err)
			return
		}
		c.renderOrganisation(w, r, http.StatusUnprocessableEntity, func(data *organisationPage) {
			data.Problem = c.problemOf(data.page, err)
		})
		return
	}
	// The logo change has no address of its own to go to from here — the
	// upload answers with nothing — so the organisation's page, which lists
	// what is waiting, is where it is seen.
	redirect(w, r, "/organisations/"+id+"#pending", "proposed")
}

func (c *console) proposeOrganisationDeletion(w http.ResponseWriter, r *http.Request) {
	change, err := c.staff(r).ProposeOrganisationDeletion(r.Context(), r.PathValue("id"))
	if err != nil {
		if apiclient.IsNotFound(err) {
			c.fail(w, r, err)
			return
		}
		c.renderOrganisation(w, r, http.StatusUnprocessableEntity, func(data *organisationPage) {
			data.Problem = c.problemOf(data.page, err)
		})
		return
	}
	redirect(w, r, "/changes/"+change.ID, "proposed")
}

// changePage shows one change: what it would do, who voted how, and the vote
// this editor may cast.
type changePage struct {
	page
	Change apiclient.Change

	// Comment is what was typed in a refused vote.
	Comment string
}

func (c *console) change(w http.ResponseWriter, r *http.Request) {
	c.renderChange(w, r, http.StatusOK, nil)
}

func (c *console) renderChange(w http.ResponseWriter, r *http.Request, status int, adjust func(*changePage)) {
	change, err := c.staff(r).Change(r.Context(), r.PathValue("id"))
	if err != nil {
		c.fail(w, r, err)
		return
	}
	data := changePage{page: c.newPage(r, "change.title")}
	data.Change = change
	if adjust != nil {
		adjust(&data)
	}
	c.renderer.Render(w, status, "change", data)
}

func (c *console) vote(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	id := r.PathValue("id")
	comment := r.FormValue("comment")
	approve := r.FormValue("vote") == "approve"

	if _, err := c.staff(r).VoteOnChange(r.Context(), id, approve, comment); err != nil {
		if apiclient.IsNotFound(err) {
			c.fail(w, r, err)
			return
		}
		c.renderChange(w, r, http.StatusUnprocessableEntity, func(data *changePage) {
			data.Comment = comment
			data.Problem = c.problemOf(data.page, err)
		})
		return
	}
	redirect(w, r, "/changes/"+id, "voted")
}

func (c *console) withdraw(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := c.staff(r).WithdrawChange(r.Context(), id); err != nil {
		if apiclient.IsNotFound(err) {
			c.fail(w, r, err)
			return
		}
		c.renderChange(w, r, http.StatusUnprocessableEntity, func(data *changePage) {
			data.Problem = c.problemOf(data.page, err)
		})
		return
	}
	redirect(w, r, "/changes/"+id, "withdrawn")
}
