package frontend

import (
	"testing"
	"time"

	"github.com/ylallemant/schmerz-reformen/internal/apiclient"
)

func berlin(t *testing.T) *time.Location {
	t.Helper()
	zone, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}
	return zone
}

// TestTheGridIsWholeWeeksStartingOnMonday: a rectangle, so the days either
// side of the month fill out the first and last row.
func TestTheGridIsWholeWeeksStartingOnMonday(t *testing.T) {
	zone := berlin(t)

	for _, tc := range []struct {
		month     string
		wantFirst string
		wantLast  string // the first day *after* the grid
		wantDays  int
	}{
		// October 2026 starts on a Thursday and ends on a Saturday.
		{"2026-10", "2026-09-28", "2026-11-02", 35},
		// February 2027 starts on a Monday and is exactly four weeks: no
		// padding at either end.
		{"2027-02", "2027-02-01", "2027-03-01", 28},
		// August 2026 starts on a Saturday and ends on a Monday: six rows.
		{"2026-08", "2026-07-27", "2026-09-07", 42},
	} {
		month, err := time.ParseInLocation(monthLayout, tc.month, zone)
		if err != nil {
			t.Fatalf("parse %s: %v", tc.month, err)
		}
		first, last := gridBounds(month)

		if got := first.Format(time.DateOnly); got != tc.wantFirst {
			t.Errorf("%s: grid starts %s, want %s", tc.month, got, tc.wantFirst)
		}
		if got := last.Format(time.DateOnly); got != tc.wantLast {
			t.Errorf("%s: grid ends before %s, want %s", tc.month, got, tc.wantLast)
		}
		if first.Weekday() != time.Monday {
			t.Errorf("%s: the grid starts on a %s", tc.month, first.Weekday())
		}

		days := monthGrid(month, first, last, month, nil, zone)
		if len(days) != tc.wantDays || len(days)%7 != 0 {
			t.Errorf("%s: %d days in the grid, want %d", tc.month, len(days), tc.wantDays)
		}
	}
}

// TestTheGridSurvivesTheNightTheClocksChange.
//
// On the last Sunday of October a day in Germany is twenty-five hours long,
// and on the last Sunday of March twenty-three. A grid built by adding
// twenty-four hours shows one date twice in autumn and skips one in spring.
func TestTheGridSurvivesTheNightTheClocksChange(t *testing.T) {
	zone := berlin(t)

	for _, name := range []string{"2026-10", "2026-03", "2027-03"} {
		month, _ := time.ParseInLocation(monthLayout, name, zone)
		first, last := gridBounds(month)
		days := monthGrid(month, first, last, month, nil, zone)

		seen := map[string]bool{}
		for i, day := range days {
			key := day.Date.Format(time.DateOnly)
			if seen[key] {
				t.Errorf("%s: %s is in the grid twice", name, key)
			}
			seen[key] = true

			if i > 0 {
				previous := days[i-1].Date
				if want := previous.AddDate(0, 0, 1).Format(time.DateOnly); key != want {
					t.Errorf("%s: %s follows %s, want %s", name, key, previous.Format(time.DateOnly), want)
				}
			}
		}
	}
}

// TestAnActionIsOnTheDayItHappensWhereItHappens, not on the day it happens in
// UTC: 00:30 on the 4th in Düsseldorf is still the 3rd in UTC, and it is the
// 4th anybody is going to turn up on.
func TestAnActionIsOnTheDayItHappensWhereItHappens(t *testing.T) {
	zone := berlin(t)
	month, _ := time.ParseInLocation(monthLayout, "2026-10", zone)
	first, last := gridBounds(month)

	late := apiclient.Action{ID: "late", Title: "Nachtwache",
		StartsAt: time.Date(2026, 10, 3, 22, 30, 0, 0, time.UTC)} // 00:30 on the 4th in Berlin
	noon := apiclient.Action{ID: "noon", Title: "Demo",
		StartsAt: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)} // 14:00 on the 3rd
	nextMonth := apiclient.Action{ID: "next", Title: "Treffen",
		StartsAt: time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)} // in the grid's last row

	today := time.Date(2026, 10, 3, 9, 0, 0, 0, zone)
	days := monthGrid(month, first, last, today, []apiclient.Action{noon, late, nextMonth}, zone)

	on := map[string][]string{}
	for _, day := range days {
		for _, action := range day.Actions {
			on[day.Date.Format(time.DateOnly)] = append(on[day.Date.Format(time.DateOnly)], action.ID)
		}
		if got, want := day.Today, day.Date.Format(time.DateOnly) == "2026-10-03"; got != want {
			t.Errorf("%s: today = %v", day.Date.Format(time.DateOnly), got)
		}
	}

	if len(on["2026-10-03"]) != 1 || on["2026-10-03"][0] != "noon" {
		t.Errorf("the 3rd holds %v, want the noon demonstration only", on["2026-10-03"])
	}
	if len(on["2026-10-04"]) != 1 || on["2026-10-04"][0] != "late" {
		t.Errorf("the 4th holds %v, want the action that starts after midnight there", on["2026-10-04"])
	}
	// The grid's padding days carry their actions too…
	if len(on["2026-11-01"]) != 1 {
		t.Errorf("the 1st of the next month holds %v, want its action shown in the last row", on["2026-11-01"])
	}

	// …and the list below it does not: an action from next month under this
	// month's heading would simply be in the wrong place.
	agenda := agendaOf(days)
	if len(agenda) != 2 {
		t.Fatalf("the agenda has %d days, want the two in October", len(agenda))
	}
	for _, day := range agenda {
		if day.Date.Month() != time.October {
			t.Errorf("the agenda lists %s under October", day.Date.Format(time.DateOnly))
		}
	}
}

// TestAMonthNobodyCouldMeanIsThisMonth.
func TestAMonthNobodyCouldMeanIsThisMonth(t *testing.T) {
	zone := berlin(t)
	today := time.Date(2026, 10, 15, 12, 0, 0, 0, zone)

	for value, want := range map[string]string{
		"":           "2026-10",
		"2026-12":    "2026-12",
		"2025-01":    "2025-01",
		"october":    "2026-10",
		"2026-13":    "2026-10",
		"0001-01":    "2026-10",
		"9000-01":    "2026-10",
		"2026-10-03": "2026-10",
	} {
		if got := monthFrom(value, today).Format(monthLayout); got != want {
			t.Errorf("monthFrom(%q) = %s, want %s", value, got, want)
		}
	}
}
