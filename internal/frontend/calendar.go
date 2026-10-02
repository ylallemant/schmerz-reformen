package frontend

import (
	"net/http"
	"time"

	"github.com/ylallemant/schmerz-reformen/internal/apiclient"
)

// calendarDay is one cell of the month grid.
type calendarDay struct {
	Date time.Time

	// Outside is whether the day belongs to the month before or after, shown
	// to fill out the first and last week.
	Outside bool
	Today   bool

	Actions []apiclient.Action
}

// agendaDay is one day of the list below the grid: the date once, then what
// happens on it.
type agendaDay struct {
	Date    time.Time
	Actions []apiclient.Action
}

// calendarPage is a month of actions, as a grid and as a list.
//
// Both, because people think about time in two ways: "is anything on the
// Saturday?" wants a grid, and "what is next?" wants a list. On a phone the
// grid is hidden by the stylesheet and the list carries the page.
type calendarPage struct {
	page

	// Month is the first day of the month shown.
	Month time.Time

	// Previous and Next are the neighbouring months as "2006-01", for the
	// links either side of the heading.
	Previous  string
	NextMonth string
	Current   string

	// Weekdays are the column headings, Monday first, as weekday numbers.
	Weekdays []int

	Days   []calendarDay
	Agenda []agendaDay

	// Mine is whether the reader asked for the actions they are coming to
	// rather than everybody's.
	Mine bool
}

// monthLayout is how a month travels in a query string.
const monthLayout = "2006-01"

// maxCalendarActions bounds one month. A month with more than this in it is
// not one a grid can show anyway.
const maxCalendarActions = 500

func (s *site) calendar(w http.ResponseWriter, r *http.Request) {
	data := calendarPage{page: s.newPage(r, "calendar.title")}
	data.Mine = r.URL.Query().Get("mine") != ""

	if data.Mine && !data.SignedIn {
		s.toSignin(w, r, r.URL.RequestURI())
		return
	}

	today := now().In(s.zone)
	data.Month = monthFrom(r.URL.Query().Get("month"), today)
	data.Current = data.Month.Format(monthLayout)
	data.Previous = data.Month.AddDate(0, -1, 0).Format(monthLayout)
	data.NextMonth = data.Month.AddDate(0, 1, 0).Format(monthLayout)
	// Monday first: this is a calendar for Germany, where the week starts on
	// the day the Montagsdemo is.
	data.Weekdays = []int{1, 2, 3, 4, 5, 6, 0}

	first, last := gridBounds(data.Month)

	// The reader's own client, so each action says whether they are coming.
	// The listing itself is the same for everybody and comes from the cache;
	// only that one flag is theirs.
	actions, _, err := s.reader(r).ListActions(r.Context(), apiclient.ActionFilter{
		From:  first,
		To:    last,
		Mine:  data.Mine,
		Limit: maxCalendarActions,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}

	data.Days = monthGrid(data.Month, first, last, today, actions, s.zone)
	data.Agenda = agendaOf(data.Days)
	s.renderer.Render(w, http.StatusOK, "calendar", data)
}

// monthFrom reads which month is wanted, falling back to the current one.
//
// A month nobody could mean — the year 0, the year 9000 — is the current one
// too: the links only ever go one month either way, so anything far out was
// typed, and a calendar for the year 9000 is a request for an empty page with
// extra arithmetic.
func monthFrom(value string, today time.Time) time.Time {
	current := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, today.Location())

	parsed, err := time.ParseInLocation(monthLayout, value, today.Location())
	if err != nil {
		return current
	}
	if parsed.Year() < today.Year()-20 || parsed.Year() > today.Year()+20 {
		return current
	}
	return parsed
}

// gridBounds returns the first instant of the grid and the first instant
// after it: the Monday on or before the 1st, and the Monday after the last
// day.
//
// Whole weeks rather than the month itself, so the grid is a rectangle and
// the days of the neighbouring months that fill it out carry their actions
// too — a demonstration on the 1st is not a surprise to somebody looking at
// the last row of the month before.
func gridBounds(month time.Time) (first, last time.Time) {
	first = month
	// Go's Sunday is 0; a week here starts on Monday.
	for first.Weekday() != time.Monday {
		first = first.AddDate(0, 0, -1)
	}

	last = month.AddDate(0, 1, 0)
	for last.Weekday() != time.Monday {
		last = last.AddDate(0, 0, 1)
	}
	return first, last
}

// monthGrid lays actions out on the days of the grid.
//
// Days are stepped with AddDate rather than by adding twenty-four hours: on
// the two nights a year the clocks change, a day is twenty-three or
// twenty-five hours long, and a grid built by adding hours would show one
// date twice or skip one.
func monthGrid(month, first, last, today time.Time, actions []apiclient.Action, zone *time.Location) []calendarDay {
	byDay := map[string][]apiclient.Action{}
	for _, action := range actions {
		key := action.StartsAt.In(zone).Format(time.DateOnly)
		byDay[key] = append(byDay[key], action)
	}

	todayKey := today.Format(time.DateOnly)

	var days []calendarDay
	for day := first; day.Before(last); day = day.AddDate(0, 0, 1) {
		key := day.Format(time.DateOnly)
		days = append(days, calendarDay{
			Date:    day,
			Outside: day.Month() != month.Month(),
			Today:   key == todayKey,
			Actions: byDay[key],
		})
	}
	return days
}

// agendaOf lists the days of the month that have something on them.
//
// Only the month itself, not the days that fill out the grid: in a list
// there is no rectangle to complete, and an action from the 1st of next month
// under this month's heading would simply be in the wrong place.
func agendaOf(days []calendarDay) []agendaDay {
	var agenda []agendaDay
	for _, day := range days {
		if day.Outside || len(day.Actions) == 0 {
			continue
		}
		agenda = append(agenda, agendaDay{Date: day.Date, Actions: day.Actions})
	}
	return agenda
}
