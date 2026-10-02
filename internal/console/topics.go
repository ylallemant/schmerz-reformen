package console

import (
	"net/http"

	"github.com/ylallemant/schmerz-reformen/internal/apiclient"
)

func (c *console) registerTopicRoutes(mux *http.ServeMux) {
	mux.Handle("GET /collectives/{id}/topics", c.localized(c.topics))
	mux.Handle("GET /collectives/{id}/topics/new", c.localized(c.newTopic))
	mux.Handle("POST /collectives/{id}/topics", c.localized(c.createTopic))

	mux.Handle("GET /topics/{id}", c.localized(c.topic))
	mux.Handle("POST /topics/{id}", c.localized(c.saveTopic))
	mux.Handle("POST /topics/{id}/delete", c.localized(c.deleteTopic))

	mux.Handle("GET /topics/{id}/updates/new", c.localized(c.newUpdate))
	mux.Handle("POST /topics/{id}/updates", c.localized(c.createUpdate))
	mux.Handle("GET /updates/{id}", c.localized(c.update))
	mux.Handle("POST /updates/{id}", c.localized(c.saveUpdate))
	mux.Handle("POST /updates/{id}/delete", c.localized(c.deleteUpdate))
}

// topicsPage lists a collective's topics at every status.
type topicsPage struct {
	page
	Collective apiclient.Collective
	Topics     []apiclient.Topic
}

func (c *console) topics(w http.ResponseWriter, r *http.Request) {
	collective, err := c.staff(r).StaffCollective(r.Context(), r.PathValue("id"))
	if err != nil {
		c.fail(w, r, err)
		return
	}
	topics, err := c.staff(r).StaffTopics(r.Context(), collective.ID)
	if err != nil {
		c.fail(w, r, err)
		return
	}

	data := topicsPage{page: c.newPage(r, "topics.title")}
	data.Collective = collective
	data.Topics = topics
	c.renderer.Render(w, http.StatusOK, "topics", data)
}

// topicPage edits one topic, and lists the updates written on it.
type topicPage struct {
	page

	Collective apiclient.Collective
	Topic      apiclient.Topic
	Form       apiclient.TopicFields

	// Amount is the figure as it was typed. Kept as text beside the parsed
	// number, so a refused form shows "100 Mio" back to the person who typed
	// it rather than an empty field.
	Amount string

	Zoom    int
	New     bool
	Updates []apiclient.Update

	Statuses []string
	Kinds    []string
	Levels   []string
}

func topicForm(topic apiclient.Topic) apiclient.TopicFields {
	return apiclient.TopicFields{
		Kind:      topic.Kind,
		Level:     topic.Level,
		Title:     topic.Title,
		Summary:   topic.Summary,
		Body:      topic.Body,
		Amount:    topic.Amount,
		SourceURL: topic.SourceURL,
		Status:    topic.Status,
		Place: apiclient.Place{
			Latitude: topic.Latitude, Longitude: topic.Longitude, Place: topic.Place,
		},
	}
}

// topicFieldsFrom reads a topic out of a posted form. The second answer is
// false when the amount could not be read as a number.
func topicFieldsFrom(r *http.Request) (apiclient.TopicFields, bool) {
	amount, err := amountFrom(r.FormValue("amount"))
	return apiclient.TopicFields{
		Kind:      r.FormValue("kind"),
		Level:     r.FormValue("level"),
		Title:     r.FormValue("title"),
		Summary:   r.FormValue("summary"),
		Body:      r.FormValue("body"),
		Amount:    amount,
		SourceURL: r.FormValue("source_url"),
		Status:    r.FormValue("status"),
		Place:     placeFrom(r),
	}, err == nil
}

func (c *console) topicData(r *http.Request, titleKey string) topicPage {
	data := topicPage{page: c.newPage(r, titleKey)}
	data.UsesMap = true
	data.Statuses = statuses
	data.Kinds = topicKinds
	data.Levels = levels
	return data
}

func (c *console) newTopic(w http.ResponseWriter, r *http.Request) {
	c.renderNewTopic(w, r, http.StatusOK, nil)
}

func (c *console) renderNewTopic(w http.ResponseWriter, r *http.Request, status int, adjust func(*topicPage)) {
	collective, err := c.staff(r).StaffCollective(r.Context(), r.PathValue("id"))
	if err != nil {
		c.fail(w, r, err)
		return
	}

	data := c.topicData(r, "topic.new_title")
	data.New = true
	data.Collective = collective
	data.Form = apiclient.TopicFields{Kind: "cut", Level: "municipal", Status: "draft"}
	// A new topic starts where its collective is: most of what an alliance in
	// Düsseldorf campaigns on is in Düsseldorf, and a pin that is nearly
	// right is quicker to correct than a map of the whole country.
	data.Form.Latitude, data.Form.Longitude = collective.Latitude, collective.Longitude
	if adjust != nil {
		adjust(&data)
	}
	c.renderer.Render(w, status, "topic", data)
}

func (c *console) createTopic(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	fields, amountOK := topicFieldsFrom(r)

	refuse := func(problem func(page) string) {
		c.renderNewTopic(w, r, http.StatusUnprocessableEntity, func(data *topicPage) {
			data.Form = fields
			data.Amount = r.FormValue("amount")
			data.Zoom = fields.Zoom
			data.Problem = problem(data.page)
		})
	}
	if !amountOK {
		refuse(func(p page) string { return p.T("topic.amount_invalid") })
		return
	}

	created, err := c.staff(r).CreateTopic(r.Context(), r.PathValue("id"), fields)
	if err != nil {
		if apiclient.IsNotFound(err) {
			c.fail(w, r, err)
			return
		}
		refuse(func(p page) string { return c.problemOf(p, err) })
		return
	}
	redirect(w, r, "/topics/"+created.ID, "created")
}

func (c *console) topic(w http.ResponseWriter, r *http.Request) {
	c.renderTopic(w, r, http.StatusOK, nil)
}

func (c *console) renderTopic(w http.ResponseWriter, r *http.Request, status int, adjust func(*topicPage)) {
	topic, err := c.staff(r).StaffTopic(r.Context(), r.PathValue("id"))
	if err != nil {
		c.fail(w, r, err)
		return
	}
	collective, err := c.staff(r).StaffCollective(r.Context(), topic.CollectiveID)
	if err != nil {
		c.fail(w, r, err)
		return
	}
	updates, err := c.staff(r).StaffUpdates(r.Context(), topic.ID)
	if err != nil {
		c.fail(w, r, err)
		return
	}

	data := c.topicData(r, "topic.edit_title")
	data.Collective = collective
	data.Topic = topic
	data.Form = topicForm(topic)
	data.Updates = updates
	if adjust != nil {
		adjust(&data)
	}
	c.renderer.Render(w, status, "topic", data)
}

func (c *console) saveTopic(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	id := r.PathValue("id")
	fields, amountOK := topicFieldsFrom(r)

	refuse := func(problem func(page) string) {
		c.renderTopic(w, r, http.StatusUnprocessableEntity, func(data *topicPage) {
			data.Form = fields
			data.Amount = r.FormValue("amount")
			data.Zoom = fields.Zoom
			data.Problem = problem(data.page)
		})
	}
	if !amountOK {
		refuse(func(p page) string { return p.T("topic.amount_invalid") })
		return
	}

	if _, err := c.staff(r).SaveTopic(r.Context(), id, fields); err != nil {
		if apiclient.IsNotFound(err) {
			c.fail(w, r, err)
			return
		}
		refuse(func(p page) string { return c.problemOf(p, err) })
		return
	}
	redirect(w, r, "/topics/"+id, "saved")
}

func (c *console) deleteTopic(w http.ResponseWriter, r *http.Request) {
	topic, err := c.staff(r).StaffTopic(r.Context(), r.PathValue("id"))
	if err != nil {
		c.fail(w, r, err)
		return
	}
	if err := c.staff(r).DeleteTopic(r.Context(), topic.ID); err != nil {
		c.fail(w, r, err)
		return
	}
	redirect(w, r, "/collectives/"+topic.CollectiveID+"/topics", "deleted")
}

// --- updates ---

// updatePage edits one update on a topic.
type updatePage struct {
	page
	Topic    apiclient.Topic
	Update   apiclient.Update
	Form     apiclient.UpdateFields
	New      bool
	Statuses []string
}

func updateFieldsFrom(r *http.Request) apiclient.UpdateFields {
	return apiclient.UpdateFields{
		Title:     r.FormValue("title"),
		Body:      r.FormValue("body"),
		SourceURL: r.FormValue("source_url"),
		Status:    r.FormValue("status"),
	}
}

func (c *console) newUpdate(w http.ResponseWriter, r *http.Request) {
	c.renderNewUpdate(w, r, http.StatusOK, nil)
}

func (c *console) renderNewUpdate(w http.ResponseWriter, r *http.Request, status int, adjust func(*updatePage)) {
	topic, err := c.staff(r).StaffTopic(r.Context(), r.PathValue("id"))
	if err != nil {
		c.fail(w, r, err)
		return
	}

	data := updatePage{page: c.newPage(r, "update.new_title")}
	data.New = true
	data.Topic = topic
	data.Form.Status = "draft"
	data.Statuses = updateStatuses
	if adjust != nil {
		adjust(&data)
	}
	c.renderer.Render(w, status, "update", data)
}

func (c *console) createUpdate(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	topicID := r.PathValue("id")
	fields := updateFieldsFrom(r)

	if _, err := c.staff(r).CreateUpdate(r.Context(), topicID, fields); err != nil {
		if apiclient.IsNotFound(err) {
			c.fail(w, r, err)
			return
		}
		c.renderNewUpdate(w, r, http.StatusUnprocessableEntity, func(data *updatePage) {
			data.Form = fields
			data.Problem = c.problemOf(data.page, err)
		})
		return
	}
	redirect(w, r, "/topics/"+topicID+"#updates", "created")
}

func (c *console) update(w http.ResponseWriter, r *http.Request) {
	c.renderUpdate(w, r, http.StatusOK, nil)
}

func (c *console) renderUpdate(w http.ResponseWriter, r *http.Request, status int, adjust func(*updatePage)) {
	update, err := c.staff(r).StaffUpdate(r.Context(), r.PathValue("id"))
	if err != nil {
		c.fail(w, r, err)
		return
	}
	topic, err := c.staff(r).StaffTopic(r.Context(), update.TopicID)
	if err != nil {
		c.fail(w, r, err)
		return
	}

	data := updatePage{page: c.newPage(r, "update.edit_title")}
	data.Topic = topic
	data.Update = update
	data.Form = apiclient.UpdateFields{
		Title: update.Title, Body: update.Body, SourceURL: update.SourceURL, Status: update.Status,
	}
	data.Statuses = updateStatuses
	if adjust != nil {
		adjust(&data)
	}
	c.renderer.Render(w, status, "update", data)
}

func (c *console) saveUpdate(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	id := r.PathValue("id")
	fields := updateFieldsFrom(r)

	saved, err := c.staff(r).SaveUpdate(r.Context(), id, fields)
	if err != nil {
		if apiclient.IsNotFound(err) {
			c.fail(w, r, err)
			return
		}
		c.renderUpdate(w, r, http.StatusUnprocessableEntity, func(data *updatePage) {
			data.Form = fields
			data.Problem = c.problemOf(data.page, err)
		})
		return
	}
	redirect(w, r, "/topics/"+saved.TopicID+"#updates", "saved")
}

func (c *console) deleteUpdate(w http.ResponseWriter, r *http.Request) {
	update, err := c.staff(r).StaffUpdate(r.Context(), r.PathValue("id"))
	if err != nil {
		c.fail(w, r, err)
		return
	}
	if err := c.staff(r).DeleteUpdate(r.Context(), update.ID); err != nil {
		c.fail(w, r, err)
		return
	}
	redirect(w, r, "/topics/"+update.TopicID+"#updates", "deleted")
}
