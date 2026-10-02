// Package syndication renders the site's content for software rather than
// people: an iCalendar file for a calendar application and an Atom feed for a
// feed reader.
//
// Both exist for the same reason. Somebody organising against a cut should not
// have to come back to this site to find out what is happening: the action
// belongs in the calendar they already look at every morning, and the news in
// the reader they already open. A site that can only be read by visiting it
// is a site that depends on being remembered.
//
// The package is pure rendering — values in, bytes out — and knows nothing of
// HTTP or of where the values came from, which is what makes it testable
// against the formats' own rules.
package syndication

import (
	"bytes"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Event is one entry of a calendar.
type Event struct {
	// UID identifies the event for ever. A calendar application uses it to
	// recognise an event it already has, so the same action must always
	// produce the same one — or every refresh would add a duplicate.
	UID string

	Summary     string
	Description string
	Location    string

	// URL is the event's own page.
	URL string

	Start time.Time

	// End is optional. Zero leaves it out, which a calendar reads as an event
	// with a start and no stated end — the truth about most demonstrations.
	End time.Time

	// Latitude and Longitude place it on a map. Both zero leaves GEO out.
	Latitude  float64
	Longitude float64

	// Cancelled marks the event as called off. It stays in the file: a
	// calendar that already holds it has to be told, and an event that merely
	// vanished from a feed is, to most applications, an event that still
	// stands.
	Cancelled bool

	// Updated is when the event last changed, so a client knows whether its
	// copy is stale. Created is when it was first published.
	Updated time.Time
	Created time.Time
}

// Calendar is a set of events with a name.
type Calendar struct {
	// Name is what a calendar application lists the subscription as.
	Name string

	// ProductID identifies what wrote the file, as the format requires.
	ProductID string

	Events []Event
}

// icsTime is the UTC form: "20261003T120000Z".
//
// Always UTC, never a local time with a TZID. A TZID has to be defined in the
// same file by a VTIMEZONE block describing the zone's rules, and a file that
// names a zone it does not define is read differently by every client. An
// instant in UTC is read the same way by all of them, and each shows it in
// its owner's own zone — which is what somebody travelling wants anyway.
const icsTime = "20060102T150405Z"

// ICS renders a calendar as an iCalendar (RFC 5545) document.
func (c Calendar) ICS() []byte {
	var b bytes.Buffer

	line(&b, "BEGIN", "VCALENDAR")
	line(&b, "VERSION", "2.0")
	line(&b, "PRODID", escapeText(c.ProductID))
	line(&b, "CALSCALE", "GREGORIAN")
	// PUBLISH: this is a calendar somebody reads, not an invitation anybody
	// is expected to answer.
	line(&b, "METHOD", "PUBLISH")
	if c.Name != "" {
		// Not in the RFC and understood by nearly everything: it is what
		// stops a subscription being listed under its URL.
		line(&b, "X-WR-CALNAME", escapeText(c.Name))
		line(&b, "NAME", escapeText(c.Name))
	}

	stamp := time.Now().UTC().Format(icsTime)
	for _, event := range c.Events {
		line(&b, "BEGIN", "VEVENT")
		line(&b, "UID", escapeText(event.UID))

		// DTSTAMP is when this representation was made; it is required.
		line(&b, "DTSTAMP", stamp)
		line(&b, "DTSTART", event.Start.UTC().Format(icsTime))
		if !event.End.IsZero() && event.End.After(event.Start) {
			line(&b, "DTEND", event.End.UTC().Format(icsTime))
		}
		if !event.Created.IsZero() {
			line(&b, "CREATED", event.Created.UTC().Format(icsTime))
		}
		if !event.Updated.IsZero() {
			line(&b, "LAST-MODIFIED", event.Updated.UTC().Format(icsTime))
			// SEQUENCE has to rise whenever the event changes, or a client
			// that already has it keeps the old time. The modification time
			// in seconds rises exactly when it should and needs no counter
			// stored anywhere.
			line(&b, "SEQUENCE", strconv.FormatInt(event.Updated.Unix(), 10))
		}

		line(&b, "SUMMARY", escapeText(event.Summary))
		if event.Description != "" {
			line(&b, "DESCRIPTION", escapeText(event.Description))
		}
		if event.Location != "" {
			line(&b, "LOCATION", escapeText(event.Location))
		}
		if event.Latitude != 0 || event.Longitude != 0 {
			line(&b, "GEO",
				strconv.FormatFloat(event.Latitude, 'f', 6, 64)+";"+
					strconv.FormatFloat(event.Longitude, 'f', 6, 64))
		}
		if event.URL != "" {
			// A URI is not TEXT and is not escaped as one: a comma in a query
			// string must stay a comma.
			line(&b, "URL", event.URL)
		}

		if event.Cancelled {
			line(&b, "STATUS", "CANCELLED")
		} else {
			line(&b, "STATUS", "CONFIRMED")
		}
		line(&b, "END", "VEVENT")
	}

	line(&b, "END", "VCALENDAR")
	return b.Bytes()
}

// escapeText escapes a TEXT value: backslash, semicolon and comma are
// escaped, and a line break becomes the two characters `\n`.
//
// This is not cosmetic. An unescaped line break in a title would end the
// property and start a new line of the editor's choosing — which is to say,
// whoever writes an action's title could add properties to the event, or
// events to the calendar, in every application subscribed to it.
func escapeText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")

	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case ';':
			b.WriteString(`\;`)
		case ',':
			b.WriteString(`\,`)
		case '\n':
			b.WriteString(`\n`)
		default:
			// Other control characters have no business in a calendar and
			// some clients choke on them.
			if r < 0x20 {
				continue
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// maxLineOctets is where a content line must be folded. The limit is in
// octets, not characters.
const maxLineOctets = 75

// line writes one property, folded.
//
// A line longer than 75 octets is continued on the next, which begins with a
// single space. The fold must not land inside a multi-byte character: a
// client unfolds by deleting "CRLF space", and one that was cut in half would
// be two invalid bytes on either side of the join.
func line(b *bytes.Buffer, name, value string) {
	content := name + ":" + value

	limit := maxLineOctets
	for len(content) > limit {
		cut := limit
		// Back up to the start of a character.
		for cut > 0 && !utf8.RuneStart(content[cut]) {
			cut--
		}
		b.WriteString(content[:cut])
		b.WriteString("\r\n ")
		content = content[cut:]
		// The continuation's leading space counts towards its own 75.
		limit = maxLineOctets - 1
	}
	b.WriteString(content)
	b.WriteString("\r\n")
}
