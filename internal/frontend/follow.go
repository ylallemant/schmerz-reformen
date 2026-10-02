package frontend

import (
	"net/http"

	"github.com/rs/zerolog/log"

	"github.com/ylallemant/schmerz-reformen/internal/apiclient"
)

// maxButtonFormBytes bounds the form behind a button: a destination to go
// back to, and nothing else.
const maxButtonFormBytes = 4 << 10

// pressed reads the small form behind a follow or an attend button, and makes
// sure there is somebody to do it for.
//
// It returns false when the request has been answered already — sent to sign
// in, or refused — and the handler should simply return.
func (s *site) pressed(w http.ResponseWriter, r *http.Request, fallback string) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxButtonFormBytes)
	if err := r.ParseForm(); err != nil {
		s.renderNotFound(w, r)
		return false
	}

	if !signedIn(r) {
		// Not an error: the button is shown to everybody, and somebody with
		// no account pressing it has just said they would like one. They are
		// sent to make it, and brought back to the page they were on — where
		// the button is still there to press.
		next := safeNext(r.PostFormValue("next"))
		if next == "" {
			next = fallback
		}
		s.toSignin(w, r, next)
		return false
	}
	return true
}

// done finishes a button press: back to the page it was pressed on.
//
// A failure is logged and the reader is sent back all the same. The page they
// land on shows the state as it now is — which, if the press did not take, is
// the button they pressed, still unpressed. That is a truthful answer, and
// the alternative is an error page between somebody and the thing they were
// reading.
func (s *site) done(w http.ResponseWriter, r *http.Request, err error, fallback string) {
	if err != nil {
		if apiclient.StatusOf(err) == http.StatusUnauthorized {
			// The session ended since the page was drawn.
			s.clearSession(w)
			s.toSignin(w, r, fallback)
			return
		}
		log.Debug().Err(err).Str("path", r.URL.Path).Msg("a reader's button press did not take")
	}
	s.back(w, r, fallback)
}

// followTarget reads what is being followed and where its page is.
//
// The target is part of the path and so arrives from the browser. It is held
// to the two things that can be followed here rather than passed along, both
// so the fallback address is one of ours and so the backend is not asked
// about a kind of thing that does not exist.
func followTarget(r *http.Request) (target, id, fallback string, ok bool) {
	target, id = r.PathValue("target"), r.PathValue("id")
	switch target {
	case "collective":
		// A collective's page is addressed by its slug, which this handler
		// does not have; the list of collectives is the nearest page that
		// certainly exists.
		return target, id, "/collectives", true
	case "topic":
		return target, id, "/topics/" + pathEscape(id), true
	}
	return "", "", "", false
}

func (s *site) follow(w http.ResponseWriter, r *http.Request) {
	target, id, fallback, ok := followTarget(r)
	if !ok {
		s.renderNotFound(w, r)
		return
	}
	if !s.pressed(w, r, fallback) {
		return
	}
	s.done(w, r, s.reader(r).Follow(r.Context(), target, id), fallback)
}

func (s *site) unfollow(w http.ResponseWriter, r *http.Request) {
	target, id, fallback, ok := followTarget(r)
	if !ok {
		s.renderNotFound(w, r)
		return
	}
	if !s.pressed(w, r, fallback) {
		return
	}
	s.done(w, r, s.reader(r).Unfollow(r.Context(), target, id), fallback)
}

func (s *site) attend(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	fallback := "/actions/" + pathEscape(id)
	if !s.pressed(w, r, fallback) {
		return
	}
	s.done(w, r, s.reader(r).Participate(r.Context(), id), fallback)
}

func (s *site) withdraw(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	fallback := "/actions/" + pathEscape(id)
	if !s.pressed(w, r, fallback) {
		return
	}
	s.done(w, r, s.reader(r).Withdraw(r.Context(), id), fallback)
}

// followingPage is what the reader follows and what they said they are coming
// to: the whole list, to the one person it belongs to.
type followingPage struct {
	page

	Following apiclient.Following
	Actions   []apiclient.Action
}

func (s *site) following(w http.ResponseWriter, r *http.Request) {
	if !signedIn(r) {
		s.toSignin(w, r, "/account/following")
		return
	}
	reader := s.reader(r)

	following, err := reader.ListFollowing(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}

	data := followingPage{page: s.newPage(r, "following.title")}
	data.Following = following

	// What they are coming to, from now on. A past action they attended is
	// not listed: this page is for deciding what to do next.
	if actions, _, err := reader.ListActions(r.Context(), apiclient.ActionFilter{Mine: true, Limit: 50}); err != nil {
		log.Warn().Err(err).Msg("cannot read the actions a reader is coming to")
	} else {
		data.Actions = actions
	}

	s.renderer.Render(w, http.StatusOK, "following", data)
}
