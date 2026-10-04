package console

import (
	"net/http"
	"strings"

	"github.com/ylallemant/schmerz-reformen/internal/apiclient"
)

func (c *console) registerCollectiveRoutes(mux *http.ServeMux) {
	mux.Handle("GET /{$}", c.localized(c.home))

	mux.Handle("GET /collectives/new", c.localized(c.newCollective))
	mux.Handle("POST /collectives", c.localized(c.createCollective))
	mux.Handle("GET /collectives/{id}", c.localized(c.collective))
	mux.Handle("POST /collectives/{id}", c.localized(c.saveCollective))
	mux.Handle("POST /collectives/{id}/logo", c.localized(c.saveCollectiveLogo))
	mux.Handle("POST /collectives/{id}/delete", c.localized(c.deleteCollective))

	mux.Handle("POST /collectives/{id}/members", c.localized(c.createMember))
	mux.Handle("POST /collectives/{id}/people", c.localized(c.saveCollectivePeople))
	mux.Handle("GET /collectives/{id}/members/{member}", c.localized(c.member))
	mux.Handle("POST /collectives/{id}/members/{member}", c.localized(c.saveMember))
	mux.Handle("POST /collectives/{id}/members/{member}/delete", c.localized(c.deleteMember))
}

// homePage lists the collectives the signed-in editor may manage.
type homePage struct {
	page
	Collectives []apiclient.Collective

	// Organisations are those the editor administers or belongs to.
	Organisations []apiclient.Organisation
}

func (c *console) home(w http.ResponseWriter, r *http.Request) {
	profile, err := c.profile(r)
	if err != nil {
		c.fail(w, r, err)
		return
	}

	data := homePage{page: c.newPage(r, "home.title")}
	data.Collectives = profile.Collectives
	data.Organisations = profile.Organisations
	c.renderer.Render(w, http.StatusOK, "home", data)
}

// collectivePage edits one collective: its profile, its logo and the
// organisations in it.
type collectivePage struct {
	page

	// Collective is what is stored; Form is what the fields show. They differ
	// only after a refused save, when the form keeps what was typed.
	Collective apiclient.Collective
	Form       apiclient.CollectiveFields

	// Zoom is what the map picker opens at.
	Zoom int

	// New is whether this is the form that creates one.
	New bool

	Statuses []string

	// Organisations are what the member picker offers. Loaded only for a
	// collective that exists: the form that creates one has no members yet.
	Organisations []apiclient.Organisation

	// People is who administers it and who writes for it, and Users who can
	// be given a role — filled for its administrators only.
	People apiclient.EntryPeople
	Users  []apiclient.User

	// PeopleProblem says why People could not be read.
	PeopleProblem string
}

func collectiveForm(collective apiclient.Collective) apiclient.CollectiveFields {
	return apiclient.CollectiveFields{
		Name:        collective.Name,
		Slug:        collective.Slug,
		Summary:     collective.Summary,
		Description: collective.Description,
		Website:     collective.Website,
		Contact:     collective.Contact,
		Status:      collective.Status,
		Place: apiclient.Place{
			Latitude: collective.Latitude, Longitude: collective.Longitude, Place: collective.Place,
		},
	}
}

func collectiveFieldsFrom(r *http.Request) apiclient.CollectiveFields {
	return apiclient.CollectiveFields{
		Name:        r.FormValue("name"),
		Slug:        r.FormValue("slug"),
		Summary:     r.FormValue("summary"),
		Description: r.FormValue("description"),
		Website:     r.FormValue("website"),
		Contact:     r.FormValue("contact"),
		Status:      r.FormValue("status"),
		Place:       placeFrom(r),
	}
}

func (c *console) collectiveData(r *http.Request, titleKey string) collectivePage {
	data := collectivePage{page: c.newPage(r, titleKey)}
	data.UsesMap = true
	data.Statuses = statuses
	return data
}

func (c *console) newCollective(w http.ResponseWriter, r *http.Request) {
	data := c.collectiveData(r, "collective.new_title")
	if !data.Admin {
		// The backend would refuse the post anyway. Saying so before somebody
		// has filled the form in is the kinder order to learn it in.
		refused := c.newPage(r, "error.forbidden_title")
		refused.Problem = refused.T("error.forbidden")
		c.renderer.Render(w, http.StatusForbidden, "problem", refused)
		return
	}
	data.New = true
	data.Form.Status = "draft"
	c.renderer.Render(w, http.StatusOK, "collective", data)
}

func (c *console) createCollective(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	fields := collectiveFieldsFrom(r)

	created, err := c.staff(r).CreateCollective(r.Context(), fields)
	if err != nil {
		data := c.collectiveData(r, "collective.new_title")
		data.New = true
		data.Form = fields
		data.Zoom = fields.Zoom
		data.Problem = c.problemOf(data.page, err)
		c.renderer.Render(w, http.StatusUnprocessableEntity, "collective", data)
		return
	}
	redirect(w, r, "/collectives/"+created.ID, "created")
}

func (c *console) collective(w http.ResponseWriter, r *http.Request) {
	c.renderCollective(w, r, http.StatusOK, nil)
}

// renderCollective draws the edit page, letting the caller adjust it — which
// is how a refused save puts back what was typed, with the reason above it.
func (c *console) renderCollective(w http.ResponseWriter, r *http.Request, status int, adjust func(*collectivePage)) {
	collective, err := c.staff(r).StaffCollective(r.Context(), r.PathValue("id"))
	if err != nil {
		c.fail(w, r, err)
		return
	}

	client := c.staff(r)
	organisations, err := client.Organisations(r.Context())
	if err != nil {
		c.fail(w, r, err)
		return
	}

	data := c.collectiveData(r, "collective.edit_title")
	data.Collective = collective
	data.Form = collectiveForm(collective)
	data.Organisations = availableOrganisations(organisations, collective.Members)
	if collective.Administers {
		data.People, data.Users, data.PeopleProblem = c.entryPeople(r, data.page, func() (apiclient.EntryPeople, error) {
			return client.CollectivePeople(r.Context(), collective.ID)
		})
	}
	if adjust != nil {
		adjust(&data)
	}
	c.renderer.Render(w, status, "collective", data)
}

func (c *console) saveCollective(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	id := r.PathValue("id")
	fields := collectiveFieldsFrom(r)

	if _, err := c.staff(r).SaveCollective(r.Context(), id, fields); err != nil {
		if apiclient.IsNotFound(err) {
			c.fail(w, r, err)
			return
		}
		c.renderCollective(w, r, http.StatusUnprocessableEntity, func(data *collectivePage) {
			data.Form = fields
			data.Zoom = fields.Zoom
			data.Problem = c.problemOf(data.page, err)
		})
		return
	}
	redirect(w, r, "/collectives/"+id, "saved")
}

func (c *console) saveCollectiveLogo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Read the upload before anything else looks at the form: a multipart
	// body can only be parsed once, and the "remove" button is a field in it.
	data, contentType, uploadErr := uploadFrom(w, r, "logo")

	var err error
	switch {
	case r.FormValue("remove") != "":
		err = c.staff(r).DeleteCollectiveLogo(r.Context(), id)
	case uploadErr != nil:
		c.renderCollective(w, r, http.StatusUnprocessableEntity, func(data *collectivePage) {
			data.Problem = data.T("error.upload")
		})
		return
	default:
		err = c.staff(r).UploadCollectiveLogo(r.Context(), id, contentType, data)
	}

	if err != nil {
		if apiclient.IsNotFound(err) {
			c.fail(w, r, err)
			return
		}
		c.renderCollective(w, r, http.StatusUnprocessableEntity, func(data *collectivePage) {
			data.Problem = c.problemOf(data.page, err)
		})
		return
	}
	redirect(w, r, "/collectives/"+id, "logo")
}

func (c *console) deleteCollective(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	id := r.PathValue("id")

	// The collective's own address has to be typed to delete it. Everything
	// it ever published goes with it and cannot be brought back, and a button
	// alone is one stray click from that.
	collective, err := c.staff(r).StaffCollective(r.Context(), id)
	if err != nil {
		c.fail(w, r, err)
		return
	}
	if strings.TrimSpace(r.FormValue("confirm")) != collective.Slug {
		c.renderCollective(w, r, http.StatusUnprocessableEntity, func(data *collectivePage) {
			data.Problem = data.T("collective.delete_mismatch")
		})
		return
	}

	if err := c.staff(r).DeleteCollective(r.Context(), id); err != nil {
		if apiclient.IsRefusal(err) && !apiclient.IsNotFound(err) {
			c.renderCollective(w, r, http.StatusUnprocessableEntity, func(data *collectivePage) {
				data.Problem = c.problemOf(data.page, err)
			})
			return
		}
		c.fail(w, r, err)
		return
	}
	redirect(w, r, "/", "deleted")
}

func (c *console) saveCollectivePeople(w http.ResponseWriter, r *http.Request) {
	c.saveEntryPeople(w, r, "collectives", "/collectives/"+r.PathValue("id"),
		func(w http.ResponseWriter, r *http.Request, problem string) {
			c.renderCollective(w, r, http.StatusUnprocessableEntity, func(data *collectivePage) {
				data.Problem = problem
			})
		})
}

// --- member organisations ---

// availableOrganisations are those a collective can still add: every
// organisation, less the ones already in its list.
func availableOrganisations(all []apiclient.Organisation, members []apiclient.Member) []apiclient.Organisation {
	listed := make(map[string]bool, len(members))
	for _, member := range members {
		listed[member.OrganisationID] = true
	}
	available := make([]apiclient.Organisation, 0, len(all))
	for _, organisation := range all {
		if !listed[organisation.ID] {
			available = append(available, organisation)
		}
	}
	return available
}

func (c *console) createMember(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	id := r.PathValue("id")

	fields := apiclient.MemberFields{OrganisationID: strings.TrimSpace(r.FormValue("organisation_id"))}
	if _, err := c.staff(r).CreateMember(r.Context(), id, fields); err != nil {
		if apiclient.IsNotFound(err) {
			c.fail(w, r, err)
			return
		}
		c.renderCollective(w, r, http.StatusUnprocessableEntity, func(data *collectivePage) {
			data.Problem = c.problemOf(data.page, err)
		})
		return
	}
	redirect(w, r, "/collectives/"+id+"#members", "saved")
}

// memberPage is one organisation's place in a collective's list.
type memberPage struct {
	page
	Collective apiclient.Collective
	Member     apiclient.Member
	Position   int
}

// findMember reads a collective and picks one of its organisations out of it.
//
// There is no backend route for a single member, and none is needed: the
// collective's page already carries the whole list, and reading it through the
// collective means the permission check is the collective's — an organisation
// cannot be reached by guessing its identifier under somebody else's.
func (c *console) findMember(r *http.Request) (apiclient.Collective, apiclient.Member, error) {
	collective, err := c.staff(r).StaffCollective(r.Context(), r.PathValue("id"))
	if err != nil {
		return apiclient.Collective{}, apiclient.Member{}, err
	}
	for _, member := range collective.Members {
		if member.ID == r.PathValue("member") {
			return collective, member, nil
		}
	}
	return collective, apiclient.Member{}, &apiclient.Error{Status: http.StatusNotFound, Message: "no such organisation"}
}

func (c *console) member(w http.ResponseWriter, r *http.Request) {
	c.renderMember(w, r, http.StatusOK, nil)
}

func (c *console) renderMember(w http.ResponseWriter, r *http.Request, status int, adjust func(*memberPage)) {
	collective, member, err := c.findMember(r)
	if err != nil {
		c.fail(w, r, err)
		return
	}

	data := memberPage{page: c.newPage(r, "member.edit_title")}
	data.Collective = collective
	data.Member = member
	data.Position = member.Position
	if adjust != nil {
		adjust(&data)
	}
	c.renderer.Render(w, status, "member", data)
}

func (c *console) saveMember(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	// Through the collective, so the address in the URL is checked rather
	// than merely decorative: /collectives/mine/members/theirs is not a way
	// in.
	collective, member, err := c.findMember(r)
	if err != nil {
		c.fail(w, r, err)
		return
	}
	position := intField(r, "position")

	if _, err := c.staff(r).SaveMember(r.Context(), member.ID, apiclient.MemberFields{Position: position}); err != nil {
		c.renderMember(w, r, http.StatusUnprocessableEntity, func(data *memberPage) {
			data.Position = position
			data.Problem = c.problemOf(data.page, err)
		})
		return
	}
	redirect(w, r, "/collectives/"+collective.ID+"#members", "saved")
}

func (c *console) deleteMember(w http.ResponseWriter, r *http.Request) {
	collective, member, err := c.findMember(r)
	if err != nil {
		c.fail(w, r, err)
		return
	}
	if err := c.staff(r).DeleteMember(r.Context(), member.ID); err != nil {
		c.fail(w, r, err)
		return
	}
	redirect(w, r, "/collectives/"+collective.ID+"#members", "deleted")
}
