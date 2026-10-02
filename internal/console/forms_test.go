package console

import (
	"testing"
	"time"
)

// TestAnAmountIsReadTheWayPeopleWriteMoney, and a figure that cannot be read
// is an error rather than a zero: zero means "no figure was given", and
// turning a typo into that would publish a cut with its amount missing.
func TestAnAmountIsReadTheWayPeopleWriteMoney(t *testing.T) {
	for written, want := range map[string]int64{
		"":              0,
		"   ":           0,
		"100000000":     100_000_000,
		"100.000.000":   100_000_000,
		"100 000 000":   100_000_000,
		"100,000,000":   100_000_000,
		"100'000'000":   100_000_000,
		"100.000.000 €": 100_000_000,
		"€ 450.000":     450_000,
		"2500000 EUR":   2_500_000,
		"100\u00a0000":  100_000, // a non-breaking space, as a spreadsheet pastes it
		"100\u202f000":  100_000, // a narrow one, as French typography does
		" 42 ":          42,
	} {
		got, err := amountFrom(written)
		if err != nil || got != want {
			t.Errorf("amountFrom(%q) = %d, %v; want %d", written, got, err, want)
		}
	}

	for _, written := range []string{"100 Mio", "viel", "-100000", "1e9", "12,5 Mio. €", "100.000.000,50x"} {
		if got, err := amountFrom(written); err == nil {
			t.Errorf("amountFrom(%q) = %d, want it refused", written, got)
		}
	}
}

// TestATimeIsReadInTheInstallationsZone: an editor means 14:00 where the
// action happens, and the zone is what turns that into an instant every
// subscribed calendar agrees on — including across the change of clocks.
func TestATimeIsReadInTheInstallationsZone(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}

	for typed, want := range map[string]string{
		"2026-10-03T14:00":    "2026-10-03T14:00:00+02:00", // summer time
		"2026-11-07T14:00":    "2026-11-07T14:00:00+01:00", // after the clocks go back
		"2026-10-03T14:00:30": "2026-10-03T14:00:00+02:00", // seconds a browser added
		"":                    "",
	} {
		got, err := instantFrom(typed, berlin)
		if err != nil || got != want {
			t.Errorf("instantFrom(%q) = %q, %v; want %q", typed, got, err, want)
		}
	}

	for _, typed := range []string{"tomorrow", "2026-10-03", "14:00", "2026-13-40T99:99"} {
		if got, err := instantFrom(typed, berlin); err == nil {
			t.Errorf("instantFrom(%q) = %q, want it refused", typed, got)
		}
	}

	// And back: what the form shows is what was typed, not the UTC behind it.
	stored := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	if got := localValue(stored, berlin); got != "2026-10-03T14:00" {
		t.Errorf("localValue = %q, want the wall-clock time in Berlin", got)
	}
	if got := localValue(time.Time{}, berlin); got != "" {
		t.Errorf("localValue of no time = %q, want an empty field", got)
	}
}
