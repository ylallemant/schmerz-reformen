package web

import (
	"strconv"
	"strings"
	"time"
)

// The methods in this file render dates, times and money the way the reader's
// language writes them.
//
// They read their words and their word order from the catalogue rather than
// from Go: "Samstag, 3. Oktober 2026" and "Saturday, 3 October 2026" differ in
// more than the names, and a pattern compiled into the binary would be one a
// translator could not correct. What stays in Go is arithmetic.

// in converts an instant to the installation's time zone.
func (p Page) in(t time.Time) time.Time {
	if p.Zone == nil {
		return t.UTC()
	}
	return t.In(p.Zone)
}

// Date renders a day in full, with its weekday: the form a reader plans by.
func (p Page) Date(t time.Time) string {
	local := p.in(t)
	return p.Tf("format.date",
		"Weekday", p.T("weekday."+strconv.Itoa(int(local.Weekday()))),
		"Day", local.Day(),
		"Month", p.T("month."+strconv.Itoa(int(local.Month()))),
		"Year", local.Year())
}

// ShortDate renders a day without its weekday, for a line of metadata.
func (p Page) ShortDate(t time.Time) string {
	local := p.in(t)
	return p.Tf("format.date_short",
		"Day", local.Day(),
		"Month", p.T("month."+strconv.Itoa(int(local.Month()))),
		"Year", local.Year())
}

// Time renders a time of day.
func (p Page) Time(t time.Time) string {
	local := p.in(t)
	return p.Tf("format.time",
		"Hour", twoDigits(local.Hour()),
		"Minute", twoDigits(local.Minute()))
}

// DateTime renders a day and a time together.
func (p Page) DateTime(t time.Time) string {
	return p.Tf("format.datetime", "Date", p.Date(t), "Time", p.Time(t))
}

// DayOfMonth is the number in a date box.
func (p Page) DayOfMonth(t time.Time) int { return p.in(t).Day() }

// MonthShort is the abbreviated month above it.
func (p Page) MonthShort(t time.Time) string {
	return p.T("month_short." + strconv.Itoa(int(p.in(t).Month())))
}

// DayKey identifies the day an instant falls on in the installation's zone,
// so a listing can tell where one day ends and the next begins.
func (p Page) DayKey(t time.Time) string { return p.in(t).Format(time.DateOnly) }

// ISO renders an instant for a machine: the `datetime` attribute of a <time>.
func (p Page) ISO(t time.Time) string { return p.in(t).Format(time.RFC3339) }

func twoDigits(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// Money renders an amount of whole euros the way a headline would: in
// millions or billions once it is that large, with one decimal only when the
// figure needs it.
//
// "100 Mio. €" rather than "100.000.000 €". The second is more exact and is
// read by nobody: eight zeros are a row of zeros, and the point of putting a
// figure on a card is that it is taken in at a glance.
func (p Page) Money(amount int64) string {
	if amount <= 0 {
		return ""
	}

	switch {
	case amount >= 1_000_000_000:
		return p.Tf("format.money_billions", "Value", p.scaled(amount, 1_000_000_000))
	case amount >= 1_000_000:
		return p.Tf("format.money_millions", "Value", p.scaled(amount, 1_000_000))
	}
	return p.Tf("format.money", "Value", p.grouped(amount))
}

// Loss renders an amount as what it is on this site: money taken away.
//
// A real minus sign (U+2212) rather than a hyphen, joined to the figure by a
// space that does not break: a hyphen is the wrong width and the wrong
// height, and a figure that wrapped onto the next line without its sign would
// read as a gift.
func (p Page) Loss(amount int64) string {
	money := p.Money(amount)
	if money == "" {
		return ""
	}
	return "\u2212\u00a0" + money
}

// scaled divides an amount by a unit and renders it with at most one decimal,
// dropped when it is zero: 100 and 2,5, never 100,0.
func (p Page) scaled(amount, unit int64) string {
	tenths := (amount*10 + unit/2) / unit // rounded to one decimal
	whole, tenth := tenths/10, tenths%10

	out := p.grouped(whole)
	if tenth != 0 {
		out += p.T("format.decimal_mark") + strconv.FormatInt(tenth, 10)
	}
	return out
}

// grouped renders a whole number with the language's thousands separator.
func (p Page) grouped(n int64) string {
	digits := strconv.FormatInt(n, 10)
	if len(digits) <= 3 {
		return digits
	}
	separator := p.T("format.thousands_mark")

	var b strings.Builder
	lead := len(digits) % 3
	if lead > 0 {
		b.WriteString(digits[:lead])
	}
	for i := lead; i < len(digits); i += 3 {
		if b.Len() > 0 {
			b.WriteString(separator)
		}
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}

// Count renders a whole number with the language's thousands separator.
func (p Page) Count(n int64) string { return p.grouped(n) }
