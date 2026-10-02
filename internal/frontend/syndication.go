package frontend

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/apiclient"
	"github.com/ylallemant/schmerz-reformen/internal/syndication"
	"github.com/ylallemant/schmerz-reformen/internal/web"
)

// How much a feed and a calendar file carry.
const (
	// feedEntries is how many updates the Atom feed holds. A reader polls and
	// keeps what it has seen, so the feed only needs to reach back further
	// than anybody's polling interval.
	feedEntries = 50

	// calendarHorizon is how far back a subscribed calendar reaches. Not
	// "from now": an action from last week that somebody attended belongs in
	// their calendar's history, and a client that found it gone from the feed
	// would, depending on the client, delete it.
	calendarHorizon = 90 * 24 * time.Hour

	calendarEvents = 500
)

// productID identifies this software in a calendar file, as the format
// requires. It names the software, not the installation.
const productID = "-//ylallemant//schmerz-reformen//DE"

// atomFeed is the news as a feed reader takes it.
func (s *site) atomFeed(w http.ResponseWriter, r *http.Request) {
	updates, _, err := s.backend.ListUpdates(r.Context(), apiclient.UpdateFilter{Limit: feedEntries})
	if err != nil {
		log.Error().Err(err).Msg("cannot read the updates for the feed")
		http.Error(w, "the feed is not available right now", http.StatusBadGateway)
		return
	}

	feed := syndication.Feed{
		ID:    s.absolute("/feed.xml"),
		Self:  s.absolute("/feed.xml"),
		Link:  s.absolute("/feed"),
		Title: s.siteName,
	}
	for _, update := range updates {
		entry := syndication.Entry{
			ID:      s.absolute("/updates/" + url.PathEscape(update.ID)),
			Link:    s.absolute("/updates/" + url.PathEscape(update.ID)),
			Title:   update.Title,
			Summary: update.Excerpt,
			Content: update.Body,
			Updated: update.UpdatedAt,
		}
		if update.PublishedAt != nil {
			entry.Published = *update.PublishedAt
		}
		if update.Collective != nil {
			entry.Author = update.Collective.Name
		}
		if update.Topic != nil {
			entry.Category = update.Topic.Title
			// The topic leads the title: in a reader's list of headlines from
			// forty feeds, "Vertagt" says nothing and "Haushalt 2027:
			// Vertagt" says what was.
			entry.Title = update.Topic.Title + ": " + update.Title
		}
		feed.Entries = append(feed.Entries, entry)
	}

	document, err := feed.Atom()
	if err != nil {
		log.Error().Err(err).Msg("cannot render the feed")
		http.Error(w, "the feed is not available right now", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
	// Short: a reader polling every few minutes should see news within a few
	// minutes, and the backend behind this already caches the listing.
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Write(document) //nolint:errcheck
}

// eventOf turns an action into a calendar entry.
func (s *site) eventOf(action apiclient.Action) syndication.Event {
	host := "schmerz-reformen"
	if parsed, err := url.Parse(s.siteURL); err == nil && parsed.Hostname() != "" {
		host = parsed.Hostname()
	}

	event := syndication.Event{
		// The action's identifier at this site's host: the same for ever, so
		// a calendar recognises the event it already holds.
		UID:       action.ID + "@" + host,
		Summary:   action.Title,
		Location:  action.Place,
		URL:       s.absolute("/actions/" + url.PathEscape(action.ID)),
		Start:     action.StartsAt,
		Latitude:  action.Latitude,
		Longitude: action.Longitude,
		Cancelled: action.Cancelled,
		Updated:   action.UpdatedAt,
	}
	if action.EndsAt != nil {
		event.End = *action.EndsAt
	}
	if action.PublishedAt != nil {
		event.Created = *action.PublishedAt
	}

	// Who is calling for it, then what they said, then where to read more.
	// A calendar entry is opened on a phone in a tram on the way there: the
	// description has to stand without the site.
	var description []string
	if action.Collective != nil {
		description = append(description, action.Collective.Name)
	}
	if action.Description != "" {
		description = append(description, action.Description)
	}
	description = append(description, event.URL)
	event.Description = strings.Join(description, "\n\n")
	return event
}

// writeCalendar sends a calendar file.
func (s *site) writeCalendar(w http.ResponseWriter, name, filename string, actions []apiclient.Action) {
	calendar := syndication.Calendar{Name: name, ProductID: productID}
	for _, action := range actions {
		calendar.Events = append(calendar.Events, s.eventOf(action))
	}

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	// The name a browser saves it under. A fixed one from this code, never
	// built from a title: a header assembled from somebody's text is how a
	// header gets split.
	w.Header().Set("Content-Disposition", `inline; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Write(calendar.ICS()) //nolint:errcheck
}

// calendarFile is every action, for a calendar application to subscribe to.
func (s *site) calendarFile(w http.ResponseWriter, r *http.Request) {
	actions, _, err := s.backend.ListActions(r.Context(), apiclient.ActionFilter{
		From:  now().Add(-calendarHorizon),
		Limit: calendarEvents,
	})
	if err != nil {
		log.Error().Err(err).Msg("cannot read the actions for the calendar file")
		http.Error(w, "the calendar is not available right now", http.StatusBadGateway)
		return
	}
	s.writeCalendar(w, s.siteName, "schmerz-reformen.ics", actions)
}

// collectiveCalendarFile is one collective's actions.
func (s *site) collectiveCalendarFile(w http.ResponseWriter, r *http.Request) {
	collective, err := s.backend.GetCollective(r.Context(), r.PathValue("slug"))
	if err != nil {
		if !apiclient.IsNotFound(err) {
			log.Error().Err(err).Msg("cannot read a collective for its calendar file")
		}
		http.NotFound(w, r)
		return
	}

	actions, _, err := s.backend.ListActions(r.Context(), apiclient.ActionFilter{
		Collective: collective.ID,
		From:       now().Add(-calendarHorizon),
		Limit:      calendarEvents,
	})
	if err != nil {
		log.Error().Err(err).Msg("cannot read a collective's actions for its calendar file")
		http.Error(w, "the calendar is not available right now", http.StatusBadGateway)
		return
	}
	s.writeCalendar(w, collective.Name, "schmerz-reformen-"+collective.Slug+".ics", actions)
}

// actionCalendarFile is one action, for "add to my calendar".
func (s *site) actionCalendarFile(w http.ResponseWriter, r *http.Request) {
	action, err := s.backend.GetAction(r.Context(), r.PathValue("id"))
	if err != nil {
		if !apiclient.IsNotFound(err) {
			log.Error().Err(err).Msg("cannot read an action for its calendar file")
		}
		http.NotFound(w, r)
		return
	}
	s.writeCalendar(w, action.Title, "aktion.ics", []apiclient.Action{action})
}

// mapItem is one marker as the map script receives it.
//
// Everything a popup shows arrives already worded: the kind's name, the
// amount, the date. The script only places text it was handed, so there is
// one place where a date is formatted and one where a kind is named — here,
// with the reader's catalogue — and none in JavaScript.
type mapItem struct {
	// Layer is which kind of thing it is: "topic", "action" or "collective".
	// It picks the marker's shape and which toggle shows it.
	Layer string `json:"layer"`

	Latitude  float64 `json:"lat"`
	Longitude float64 `json:"lng"`

	// Href is the item's own page.
	Href string `json:"href"`

	Title string `json:"title"`

	// Glyph is what is drawn inside the marker. An action carries the day of
	// the month it happens on, so the map reads as a calendar at a glance; a
	// collective carries its initial.
	Glyph string `json:"glyph,omitempty"`

	// Tag is the kind in words, Meta a line of context under it, Figure the
	// amount where there is one, and Text the summary.
	Tag    string `json:"tag"`
	Meta   string `json:"meta,omitempty"`
	Figure string `json:"figure,omitempty"`
	Text   string `json:"text,omitempty"`

	// Cancelled marks an action that is off, so the popup can say so.
	Cancelled bool `json:"cancelled,omitempty"`
}

// mapAnswer is one viewport.
type mapAnswer struct {
	Items []mapItem `json:"items"`

	// Status is the line under the map, already worded: how much of
	// everything there is, whatever is in view.
	Status string `json:"status"`
}

// mapJSON is what the map asks for each time the reader moves it.
//
// It goes through this service rather than to the backend because the backend
// is not publicly exposed — and because this is where the reader's language
// is known.
//
// It is asked without the reader's session. Nothing on the map is about the
// reader, and a request that carried a credential for an answer that does not
// depend on it would only make the answer uncacheable.
func (s *site) mapJSON(w http.ResponseWriter, r *http.Request) {
	p := web.NewPage(r, s.localization, "")
	p.Zone = s.zone

	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	view, err := s.backend.ReadMap(r.Context(), r.URL.Query().Get("bounds"))
	if err != nil {
		// An empty map with a sentence under it, not an error the script has
		// to handle: the page around the map is still worth reading.
		log.Warn().Err(err).Msg("cannot read the map")
		json.NewEncoder(w).Encode(mapAnswer{ //nolint:errcheck
			Items: []mapItem{}, Status: p.T("map.unavailable"),
		})
		return
	}

	answer := mapAnswer{Items: []mapItem{}}
	for _, topic := range view.Topics {
		item := mapItem{
			Layer:     "topic",
			Latitude:  topic.Latitude,
			Longitude: topic.Longitude,
			Href:      "/topics/" + url.PathEscape(topic.ID),
			Title:     topic.Title,
			Glyph:     "!",
			Tag:       p.T("kind." + topic.Kind),
			Meta:      joinMeta(p.T("level."+topic.Level), topic.Place),
			Text:      topic.Summary,
		}
		if topic.Amount > 0 {
			item.Figure = p.Loss(topic.Amount)
		}
		answer.Items = append(answer.Items, item)
	}
	for _, action := range view.Actions {
		answer.Items = append(answer.Items, mapItem{
			Layer:     "action",
			Latitude:  action.Latitude,
			Longitude: action.Longitude,
			Href:      "/actions/" + url.PathEscape(action.ID),
			Title:     action.Title,
			Glyph:     strconv.Itoa(p.DayOfMonth(action.StartsAt)),
			Tag:       p.T("action_kind." + action.Kind),
			Meta:      joinMeta(p.DateTime(action.StartsAt), action.Place),
			Cancelled: action.Cancelled,
		})
	}
	for _, collective := range view.Collectives {
		answer.Items = append(answer.Items, mapItem{
			Layer:     "collective",
			Latitude:  collective.Latitude,
			Longitude: collective.Longitude,
			Href:      "/collectives/" + url.PathEscape(collective.Slug),
			Title:     collective.Name,
			Glyph:     initialOf(collective.Name),
			Tag:       p.T("map.layer_collective"),
			Meta:      collective.Place,
			Text:      collective.Summary,
		})
	}

	answer.Status = p.Tf("map.status",
		"Topics", p.Count(view.Totals.Topics),
		"Actions", p.Count(view.Totals.Actions),
		"Collectives", p.Count(view.Totals.Collectives))

	// A few seconds, shared: every reader looking at the same part of the
	// country asks the same question, and the backend invalidates its own
	// copy on a write, so this is the only staleness there is.
	w.Header().Set("Cache-Control", "public, max-age=15")
	json.NewEncoder(w).Encode(answer) //nolint:errcheck
}

// initialOf is the first letter of a name, upper-cased by the stylesheet.
func initialOf(name string) string {
	for _, r := range strings.TrimSpace(name) {
		return string(r)
	}
	return ""
}

// joinMeta joins the parts of a line of context, skipping the empty ones.
func joinMeta(parts ...string) string {
	var kept []string
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, " · ")
}
