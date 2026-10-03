package console

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/ylallemant/schmerz-reformen/internal/staffauth"
	"github.com/ylallemant/schmerz-reformen/internal/web"
)

// With authentication off, everybody is one stand-in editor — and a change to
// an organisation needs the approval of editors other than its author, which
// one person can never give themselves. So a local console offers a few
// stand-ins to switch between, each a different editor as far as the backend
// can tell. Nothing of this exists unless --development is set.

// standIns is how many stand-in editors a local console offers: the author
// and the three approvals a change needs by default.
const standIns = 4

// standInCookie remembers which stand-in this browser is.
const standInCookie = "schmerz_stand_in"

// standInNumbers lists the stand-ins by number, for the switcher.
func standInNumbers() []int {
	numbers := make([]int, standIns)
	for i := range numbers {
		numbers[i] = i + 1
	}
	return numbers
}

// standInNumber is the stand-in a request is made as: 1 unless the browser
// chose another.
func standInNumber(r *http.Request) int {
	cookie, err := r.Cookie(standInCookie)
	if err != nil {
		return 1
	}
	number, err := strconv.Atoi(cookie.Value)
	if err != nil || number < 1 || number > standIns {
		return 1
	}
	return number
}

// standIn is the identity of the stand-in a request is made as. The first is
// the configured development identity unchanged; the others share its groups
// and differ in subject and name, which is all that tells editors apart.
func (c *console) standIn(r *http.Request) staffauth.Identity {
	identity := c.developmentIdentity
	if number := standInNumber(r); number > 1 {
		identity.Subject = fmt.Sprintf("%s-%d", identity.Subject, number)
		identity.Name = fmt.Sprintf("%s %d", identity.Name, number)
	}
	return identity
}

// switchStandIn makes this browser another stand-in and sends it back.
func (c *console) switchStandIn(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	number, err := strconv.Atoi(r.FormValue("stand_in"))
	if err != nil || number < 1 || number > standIns {
		http.Error(w, "no such stand-in", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: standInCookie, Value: strconv.Itoa(number), Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})

	next := web.SafeNext(r.FormValue("next"))
	if next == "" {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}
