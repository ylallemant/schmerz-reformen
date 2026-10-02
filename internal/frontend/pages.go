package frontend

import (
	"net/http"
	"strconv"

	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/apiclient"
)

// Choices a filter offers, in the order a reader would look for them. Each is
// a key into the shared catalogue — `kind.cut`, `level.federal`.
var (
	topicKinds = []string{"cut", "reform", "closure", "privatisation", "other"}
	levels     = []string{"federal", "state", "municipal"}
)

// landingPage is the front page: the map, and what is read beside it.
type landingPage struct {
	page

	// Totals are of everything, whatever the map below happens to show: how
	// many there are is the argument.
	Totals apiclient.MapTotals

	Updates []apiclient.Update
	Actions []apiclient.Action
}

func (s *site) landing(w http.ResponseWriter, r *http.Request) {
	data := landingPage{page: s.newPage(r, "landing.title")}
	data.UsesMap = true

	// Three reads, and none of them is allowed to take the page down: a front
	// page with an empty column is a worse page, and a front page that
	// answers 502 because the feed was slow is no page at all.
	if view, err := s.backend.ReadMap(r.Context(), ""); err != nil {
		log.Warn().Err(err).Msg("cannot read the totals for the landing page")
	} else {
		data.Totals = view.Totals
	}

	if updates, _, err := s.backend.ListUpdates(r.Context(), apiclient.UpdateFilter{Limit: landingUpdates}); err != nil {
		log.Warn().Err(err).Msg("cannot read the feed for the landing page")
	} else {
		data.Updates = updates
	}

	if actions, _, err := s.reader(r).ListActions(r.Context(), apiclient.ActionFilter{Limit: landingActions}); err != nil {
		log.Warn().Err(err).Msg("cannot read the calendar for the landing page")
	} else {
		data.Actions = actions
	}

	s.renderer.Render(w, http.StatusOK, "landing", data)
}

func (s *site) about(w http.ResponseWriter, r *http.Request) {
	s.renderer.Render(w, http.StatusOK, "about", s.newPage(r, "about.title"))
}

// topicsPage lists what collectives are campaigning on.
type topicsPage struct {
	page

	Topics []apiclient.Topic
	Total  int64

	// Kind and Level are the filters in force; Kinds and Levels what is on
	// offer.
	Kind   string
	Level  string
	Kinds  []string
	Levels []string
}

func (s *site) topics(w http.ResponseWriter, r *http.Request) {
	data := topicsPage{page: s.newPage(r, "topics.title")}
	data.Kinds, data.Levels = topicKinds, levels
	data.Kind = oneOf(r.URL.Query().Get("kind"), topicKinds)
	data.Level = oneOf(r.URL.Query().Get("level"), levels)

	topics, total, err := s.backend.ListTopics(r.Context(), apiclient.TopicFilter{
		Kind: data.Kind, Level: data.Level, Limit: topicsPerPage,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data.Topics, data.Total = topics, total
	s.renderer.Render(w, http.StatusOK, "topics", data)
}

// oneOf keeps a filter value only if it is one the page offers.
//
// An unknown value is dropped rather than passed on: the backend would refuse
// it, and a reader who followed a link with a filter that no longer exists
// should get the unfiltered page, not an error.
func oneOf(value string, allowed []string) string {
	for _, candidate := range allowed {
		if value == candidate {
			return value
		}
	}
	return ""
}

// topicPage is one topic: what is planned, the news on it, and what there is
// to come to.
type topicPage struct {
	page

	Topic   apiclient.Topic
	Updates []apiclient.Update
	Actions []apiclient.Action
}

func (s *site) topic(w http.ResponseWriter, r *http.Request) {
	reader := s.reader(r)

	topic, err := reader.GetTopic(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}

	data := topicPage{page: s.newPage(r, "topic.title")}
	data.Topic = topic
	data.describe(topic.Title, topic.Summary)
	data.UsesMap = topic.Latitude != 0 || topic.Longitude != 0

	// The topic is the page; its news and its actions are parts of it, and a
	// part that could not be read is left out rather than failing the whole.
	if updates, _, err := s.backend.ListUpdates(r.Context(), apiclient.UpdateFilter{Topic: topic.ID, Limit: 50}); err != nil {
		log.Warn().Err(err).Str("topic", topic.ID).Msg("cannot read a topic's updates")
	} else {
		data.Updates = updates
	}
	if actions, _, err := reader.ListActions(r.Context(), apiclient.ActionFilter{Topic: topic.ID, Limit: 20}); err != nil {
		log.Warn().Err(err).Str("topic", topic.ID).Msg("cannot read a topic's actions")
	} else {
		data.Actions = actions
	}

	s.renderer.Render(w, http.StatusOK, "topic", data)
}

// feedPage is the news, newest first.
type feedPage struct {
	page

	Updates []apiclient.Update
	Total   int64

	// Following is whether the reader asked for their own feed rather than
	// everybody's.
	Following bool

	// Number is the page being shown, counted from one; Older and Newer are
	// the neighbours, zero when there is none.
	Number int
	Older  int
	Newer  int
}

func (s *site) feed(w http.ResponseWriter, r *http.Request) {
	data := feedPage{page: s.newPage(r, "feed.title")}
	data.Following = r.URL.Query().Get("following") != ""
	data.Number = pageNumber(r)

	if data.Following && !data.SignedIn {
		// "What I follow" with nobody to ask it about. Sent to sign in with
		// the way back attached, rather than shown everybody's feed under a
		// heading that says it is theirs.
		s.toSignin(w, r, r.URL.RequestURI())
		return
	}

	// The shared client for everybody's feed and the reader's own for theirs:
	// a public listing asked for with a session would be the same answer with
	// a credential attached for no reason.
	client := s.backend
	if data.Following {
		client = s.reader(r)
	}

	updates, total, err := client.ListUpdates(r.Context(), apiclient.UpdateFilter{
		Following: data.Following,
		Limit:     feedPerPage,
		Offset:    (data.Number - 1) * feedPerPage,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data.Updates, data.Total = updates, total

	if data.Number > 1 {
		data.Newer = data.Number - 1
	}
	if int64(data.Number*feedPerPage) < total {
		data.Older = data.Number + 1
	}
	s.renderer.Render(w, http.StatusOK, "feed", data)
}

// pageNumber reads which page of a listing is wanted. Anything that is not a
// positive number is the first.
func pageNumber(r *http.Request) int {
	number, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || number < 1 {
		return 1
	}
	// Bounded: the offset is multiplied out from this, and a page number
	// nobody could have reached by pressing "older" is somebody probing.
	if number > 5000 {
		return 5000
	}
	return number
}

// updatePage is one piece of news at an address of its own, so it can be
// sent to somebody.
type updatePage struct {
	page
	Update apiclient.Update
}

func (s *site) update(w http.ResponseWriter, r *http.Request) {
	update, err := s.backend.GetUpdate(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}

	data := updatePage{page: s.newPage(r, "update.title")}
	data.Update = update
	data.describe(update.Title, update.Excerpt)
	s.renderer.Render(w, http.StatusOK, "update", data)
}

// collectivesPage lists the alliances.
type collectivesPage struct {
	page
	Collectives []apiclient.Collective
}

func (s *site) collectives(w http.ResponseWriter, r *http.Request) {
	collectives, _, err := s.backend.ListCollectives(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}

	data := collectivesPage{page: s.newPage(r, "collectives.title")}
	data.Collectives = collectives
	s.renderer.Render(w, http.StatusOK, "collectives", data)
}

// collectivePage is one alliance: who is in it, what it campaigns on, what it
// has said and what it has announced.
type collectivePage struct {
	page

	Collective apiclient.Collective
	Topics     []apiclient.Topic
	Updates    []apiclient.Update
	Actions    []apiclient.Action
}

func (s *site) collective(w http.ResponseWriter, r *http.Request) {
	reader := s.reader(r)

	collective, err := reader.GetCollective(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.fail(w, r, err)
		return
	}

	data := collectivePage{page: s.newPage(r, "collective.title")}
	data.Collective = collective
	data.describe(collective.Name, collective.Summary)

	if topics, _, err := s.backend.ListTopics(r.Context(), apiclient.TopicFilter{Collective: collective.ID}); err != nil {
		log.Warn().Err(err).Str("collective", collective.ID).Msg("cannot read a collective's topics")
	} else {
		data.Topics = topics
	}
	if updates, _, err := s.backend.ListUpdates(r.Context(), apiclient.UpdateFilter{Collective: collective.ID, Limit: 10}); err != nil {
		log.Warn().Err(err).Str("collective", collective.ID).Msg("cannot read a collective's updates")
	} else {
		data.Updates = updates
	}
	if actions, _, err := reader.ListActions(r.Context(), apiclient.ActionFilter{Collective: collective.ID, Limit: 20}); err != nil {
		log.Warn().Err(err).Str("collective", collective.ID).Msg("cannot read a collective's actions")
	} else {
		data.Actions = actions
	}

	s.renderer.Render(w, http.StatusOK, "collective", data)
}

// actionPage is one action: when, where, and whether the reader is coming.
type actionPage struct {
	page
	Action apiclient.Action

	// Past is whether it has already started, which is when "I'm coming"
	// stops being something to offer.
	Past bool
}

func (s *site) action(w http.ResponseWriter, r *http.Request) {
	action, err := s.reader(r).GetAction(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}

	data := actionPage{page: s.newPage(r, "action.title")}
	data.Action = action
	data.describe(action.Title, data.DateTime(action.StartsAt)+" · "+action.Place)
	data.UsesMap = action.Latitude != 0 || action.Longitude != 0
	data.Past = action.StartsAt.Before(now())
	s.renderer.Render(w, http.StatusOK, "action", data)
}
