package console

import (
	"net/http"
	"time"

	"github.com/ylallemant/schmerz-reformen/internal/apiclient"
)

func (c *console) registerActionRoutes(mux *http.ServeMux) {
	mux.Handle("GET /collectives/{id}/actions", c.localized(c.actions))
	mux.Handle("GET /collectives/{id}/actions/new", c.localized(c.newAction))
	mux.Handle("POST /collectives/{id}/actions", c.localized(c.createAction))

	mux.Handle("GET /actions/{id}", c.localized(c.action))
	mux.Handle("POST /actions/{id}", c.localized(c.saveAction))
	mux.Handle("POST /actions/{id}/delete", c.localized(c.deleteAction))
}

// actionsPage lists a collective's actions at every status, latest first.
type actionsPage struct {
	page
	Collective apiclient.Collective
	Actions    []apiclient.Action

	// Now is when the page was drawn, so the listing can mark what is past.
	Now time.Time
}

func (c *console) actions(w http.ResponseWriter, r *http.Request) {
	collective, err := c.staff(r).StaffCollective(r.Context(), r.PathValue("id"))
	if err != nil {
		c.fail(w, r, err)
		return
	}
	actions, err := c.staff(r).StaffActions(r.Context(), collective.ID)
	if err != nil {
		c.fail(w, r, err)
		return
	}

	data := actionsPage{page: c.newPage(r, "actions.title")}
	data.Collective = collective
	data.Actions = actions
	data.Now = time.Now()
	c.renderer.Render(w, http.StatusOK, "actions", data)
}

// actionForm is an action as its form shows it.
//
// Its own type rather than the client's fields, because the two times are
// shown and typed as wall-clock values in the installation's zone, and sent
// as instants: the conversion happens between these two shapes and nowhere
// else.
type actionForm struct {
	Kind        string
	Title       string
	Description string
	TopicID     string
	StartsAt    string
	EndsAt      string
	ExternalURL string
	Status      string
	Cancelled   bool

	apiclient.Place
}

// actionPage edits one action.
type actionPage struct {
	page

	Collective apiclient.Collective
	Action     apiclient.Action
	Form       actionForm

	// Topics are the collective's own, which are the only ones an action may
	// be about.
	Topics []apiclient.Topic

	Zoom int
	New  bool

	Statuses []string
	Kinds    []string
}

func (c *console) actionFormOf(action apiclient.Action) actionForm {
	form := actionForm{
		Kind:        action.Kind,
		Title:       action.Title,
		Description: action.Description,
		TopicID:     action.TopicID,
		StartsAt:    localValue(action.StartsAt, c.zone),
		ExternalURL: action.ExternalURL,
		Status:      action.Status,
		Cancelled:   action.Cancelled,
		Place: apiclient.Place{
			Latitude: action.Latitude, Longitude: action.Longitude, Place: action.Place,
		},
	}
	if action.EndsAt != nil {
		form.EndsAt = localValue(*action.EndsAt, c.zone)
	}
	return form
}

func actionFormFrom(r *http.Request) actionForm {
	return actionForm{
		Kind:        r.FormValue("kind"),
		Title:       r.FormValue("title"),
		Description: r.FormValue("description"),
		TopicID:     r.FormValue("topic_id"),
		StartsAt:    r.FormValue("starts_at"),
		EndsAt:      r.FormValue("ends_at"),
		ExternalURL: r.FormValue("external_url"),
		Status:      r.FormValue("status"),
		Cancelled:   r.FormValue("cancelled") != "",
		Place:       placeFrom(r),
	}
}

// fields turns what a form holds into what the backend is sent, reading the
// two times in the installation's zone. It reports false when either is not a
// date and time.
func (c *console) fields(form actionForm) (apiclient.ActionFields, bool) {
	starts, startsErr := instantFrom(form.StartsAt, c.zone)
	ends, endsErr := instantFrom(form.EndsAt, c.zone)

	return apiclient.ActionFields{
		Kind:        form.Kind,
		Title:       form.Title,
		Description: form.Description,
		TopicID:     form.TopicID,
		StartsAt:    starts,
		EndsAt:      ends,
		ExternalURL: form.ExternalURL,
		Status:      form.Status,
		Cancelled:   form.Cancelled,
		Place:       form.Place,
	}, startsErr == nil && endsErr == nil
}

// actionData builds the page around an action's collective: its topics, and
// the choices the form offers.
func (c *console) actionData(r *http.Request, collectiveID, titleKey string) (actionPage, error) {
	collective, err := c.staff(r).StaffCollective(r.Context(), collectiveID)
	if err != nil {
		return actionPage{}, err
	}
	topics, err := c.staff(r).StaffTopics(r.Context(), collective.ID)
	if err != nil {
		return actionPage{}, err
	}

	data := actionPage{page: c.newPage(r, titleKey)}
	data.UsesMap = true
	data.Collective = collective
	data.Topics = topics
	data.Statuses = updateStatuses
	data.Kinds = actionKinds
	return data, nil
}

func (c *console) newAction(w http.ResponseWriter, r *http.Request) {
	c.renderNewAction(w, r, http.StatusOK, nil)
}

func (c *console) renderNewAction(w http.ResponseWriter, r *http.Request, status int, adjust func(*actionPage)) {
	data, err := c.actionData(r, r.PathValue("id"), "action.new_title")
	if err != nil {
		c.fail(w, r, err)
		return
	}

	data.New = true
	data.Form = actionForm{Kind: "demonstration", Status: "draft", TopicID: r.URL.Query().Get("topic")}
	// Starts on the collective's own pin, like a new topic: most actions
	// happen where the alliance is, and the editor is about to zoom in on a
	// square in that city anyway.
	data.Form.Latitude, data.Form.Longitude = data.Collective.Latitude, data.Collective.Longitude
	if adjust != nil {
		adjust(&data)
	}
	c.renderer.Render(w, status, "action", data)
}

func (c *console) createAction(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	form := actionFormFrom(r)
	fields, timesOK := c.fields(form)

	refuse := func(problem func(page) string) {
		c.renderNewAction(w, r, http.StatusUnprocessableEntity, func(data *actionPage) {
			data.Form = form
			data.Zoom = form.Zoom
			data.Problem = problem(data.page)
		})
	}
	if !timesOK {
		refuse(func(p page) string { return p.T("action.time_invalid") })
		return
	}

	created, err := c.staff(r).CreateAction(r.Context(), r.PathValue("id"), fields)
	if err != nil {
		if apiclient.IsNotFound(err) {
			c.fail(w, r, err)
			return
		}
		refuse(func(p page) string { return c.problemOf(p, err) })
		return
	}
	redirect(w, r, "/actions/"+created.ID, "created")
}

func (c *console) action(w http.ResponseWriter, r *http.Request) {
	c.renderAction(w, r, http.StatusOK, nil)
}

func (c *console) renderAction(w http.ResponseWriter, r *http.Request, status int, adjust func(*actionPage)) {
	action, err := c.staff(r).StaffAction(r.Context(), r.PathValue("id"))
	if err != nil {
		c.fail(w, r, err)
		return
	}
	data, err := c.actionData(r, action.CollectiveID, "action.edit_title")
	if err != nil {
		c.fail(w, r, err)
		return
	}

	data.Action = action
	data.Form = c.actionFormOf(action)
	if adjust != nil {
		adjust(&data)
	}
	c.renderer.Render(w, status, "action", data)
}

func (c *console) saveAction(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	id := r.PathValue("id")
	form := actionFormFrom(r)
	fields, timesOK := c.fields(form)

	refuse := func(problem func(page) string) {
		c.renderAction(w, r, http.StatusUnprocessableEntity, func(data *actionPage) {
			data.Form = form
			data.Zoom = form.Zoom
			data.Problem = problem(data.page)
		})
	}
	if !timesOK {
		refuse(func(p page) string { return p.T("action.time_invalid") })
		return
	}

	if _, err := c.staff(r).SaveAction(r.Context(), id, fields); err != nil {
		if apiclient.IsNotFound(err) {
			c.fail(w, r, err)
			return
		}
		refuse(func(p page) string { return c.problemOf(p, err) })
		return
	}
	redirect(w, r, "/actions/"+id, "saved")
}

func (c *console) deleteAction(w http.ResponseWriter, r *http.Request) {
	action, err := c.staff(r).StaffAction(r.Context(), r.PathValue("id"))
	if err != nil {
		c.fail(w, r, err)
		return
	}
	if err := c.staff(r).DeleteAction(r.Context(), action.ID); err != nil {
		c.fail(w, r, err)
		return
	}
	redirect(w, r, "/collectives/"+action.CollectiveID+"/actions", "deleted")
}
