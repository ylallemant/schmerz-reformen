package syndication

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

var start = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

func demo() Event {
	return Event{
		UID:      "action-1@schmerz.example",
		Summary:  "Demo vor dem Rathaus",
		Location: "Marktplatz 2, Düsseldorf",
		URL:      "https://schmerz.example/actions/1",
		Start:    start,
		Updated:  start.Add(-48 * time.Hour),
	}
}

// unfold undoes the line folding, the way a calendar application does: a
// CRLF followed by a space is deleted.
func unfold(document []byte) string {
	return strings.ReplaceAll(string(document), "\r\n ", "")
}

// property returns the value of the first line with the given name.
func property(document, name string) (string, bool) {
	for _, line := range strings.Split(document, "\r\n") {
		if value, found := strings.CutPrefix(line, name+":"); found {
			return value, true
		}
	}
	return "", false
}

// TestACalendarIsWhatACalendarApplicationExpects: the structure the format
// requires, with every line ended the way it requires.
func TestACalendarIsWhatACalendarApplicationExpects(t *testing.T) {
	raw := Calendar{Name: "schMERZ-Reformen", ProductID: "-//schmerz//DE", Events: []Event{demo()}}.ICS()
	document := unfold(raw)

	for _, want := range []string{
		"BEGIN:VCALENDAR\r\n", "VERSION:2.0\r\n", "PRODID:-//schmerz//DE\r\n",
		"BEGIN:VEVENT\r\n", "UID:action-1@schmerz.example\r\n",
		"DTSTART:20261003T120000Z\r\n", "SUMMARY:Demo vor dem Rathaus\r\n",
		"STATUS:CONFIRMED\r\n", "END:VEVENT\r\n", "END:VCALENDAR\r\n",
	} {
		if !strings.Contains(document, want) {
			t.Errorf("the calendar is missing %q", want)
		}
	}
	if !strings.HasSuffix(document, "END:VCALENDAR\r\n") {
		t.Error("the calendar does not end where it should")
	}
	// A bare line feed is not a line ending here, and some clients stop
	// reading at one.
	if strings.Contains(strings.ReplaceAll(string(raw), "\r\n", ""), "\n") {
		t.Error("the calendar contains a bare line feed")
	}

	// No stated end means none is written: an invented one would be a time
	// somebody plans around.
	if _, found := property(document, "DTEND"); found {
		t.Error("an event with no end was given one")
	}
}

// TestATitleCannotAddToTheCalendar is the escaping, and it is a security
// property rather than a nicety: an unescaped line break ends the property
// and starts a line of the writer's choosing, in every application
// subscribed to the feed.
func TestATitleCannotAddToTheCalendar(t *testing.T) {
	event := demo()
	event.Summary = "Demo\r\nEND:VEVENT\r\nBEGIN:VEVENT\r\nSUMMARY:Gegendemo; hier, jetzt\\"
	event.Description = "Erste Zeile\nZweite Zeile"
	event.Location = "Rathaus\rSTATUS:CANCELLED"

	document := unfold(Calendar{Events: []Event{event}}.ICS())

	// Counted as lines, which is how a calendar application reads them: the
	// words are allowed to appear inside a value, and must not begin a line.
	if got := strings.Count(document, "\r\nBEGIN:VEVENT\r\n"); got != 1 {
		t.Fatalf("the calendar has %d events, want the one that was given", got)
	}
	if got := strings.Count(document, "\r\nEND:VEVENT\r\n"); got != 1 {
		t.Fatalf("the calendar closes %d events, want one", got)
	}
	if strings.Contains(document, "\r\nSTATUS:CANCELLED") {
		t.Error("a place name cancelled the event")
	}

	summary, _ := property(document, "SUMMARY")
	want := `Demo\nEND:VEVENT\nBEGIN:VEVENT\nSUMMARY:Gegendemo\; hier\, jetzt\\`
	if summary != want {
		t.Errorf("summary = %q, want %q", summary, want)
	}
	if description, _ := property(document, "DESCRIPTION"); description != `Erste Zeile\nZweite Zeile` {
		t.Errorf("description = %q, want the line break kept as an escape", description)
	}
}

// TestLongLinesAreFoldedBetweenCharacters: the limit is 75 octets, and a fold
// in the middle of a multi-byte character is two invalid bytes either side of
// the join.
func TestLongLinesAreFoldedBetweenCharacters(t *testing.T) {
	event := demo()
	// Every character here is two or three bytes, so a fold counted in
	// characters, or placed without looking, lands inside one.
	event.Summary = strings.Repeat("Kürzungen für Bürgerinnen – ", 12)
	event.Description = strings.Repeat("社会保障の削減に反対する。", 20)

	raw := Calendar{Events: []Event{event}}.ICS()

	for _, physical := range bytes.Split(raw, []byte("\r\n")) {
		if len(physical) > 75 {
			t.Errorf("a line is %d octets, over the limit of 75: %q", len(physical), physical)
		}
		if !utf8.Valid(physical) {
			t.Errorf("a line was folded inside a character: %q", physical)
		}
	}

	// And unfolding gives back exactly what went in.
	document := unfold(raw)
	if summary, _ := property(document, "SUMMARY"); summary != event.Summary {
		t.Errorf("the summary did not survive folding:\n got %q\nwant %q", summary, event.Summary)
	}
	if description, _ := property(document, "DESCRIPTION"); description != event.Description {
		t.Error("the description did not survive folding")
	}
}

// TestACancelledActionStaysInTheCalendarAndSaysSo: an event that merely
// vanished from a feed is, to most applications, an event that still stands.
func TestACancelledActionStaysInTheCalendarAndSaysSo(t *testing.T) {
	event := demo()
	event.Cancelled = true
	document := unfold(Calendar{Events: []Event{event}}.ICS())

	if status, _ := property(document, "STATUS"); status != "CANCELLED" {
		t.Errorf("status = %q, want CANCELLED", status)
	}
	if !strings.Contains(document, "BEGIN:VEVENT") {
		t.Error("the cancelled event was left out")
	}
}

// TestAChangedActionReplacesTheCopyAClientHolds: the sequence has to rise
// with every change, or a calendar keeps the old time.
func TestAChangedActionReplacesTheCopyAClientHolds(t *testing.T) {
	before, after := demo(), demo()
	after.Start = before.Start.Add(time.Hour)
	after.Updated = before.Updated.Add(time.Minute)

	first := unfold(Calendar{Events: []Event{before}}.ICS())
	second := unfold(Calendar{Events: []Event{after}}.ICS())

	firstUID, _ := property(first, "UID")
	secondUID, _ := property(second, "UID")
	if firstUID != secondUID {
		t.Error("a changed action got a new identity: every refresh would add a duplicate")
	}

	firstSequence, _ := property(first, "SEQUENCE")
	secondSequence, _ := property(second, "SEQUENCE")
	if firstSequence == "" || firstSequence >= secondSequence {
		t.Errorf("sequence went from %q to %q, want it to rise", firstSequence, secondSequence)
	}
}

// TestAnEndIsWrittenOnlyWhenItIsOne.
func TestAnEndIsWrittenOnlyWhenItIsOne(t *testing.T) {
	event := demo()
	event.End = event.Start.Add(2 * time.Hour)
	if end, _ := property(unfold(Calendar{Events: []Event{event}}.ICS()), "DTEND"); end != "20261003T140000Z" {
		t.Errorf("DTEND = %q", end)
	}

	// An end before the start is not an end; writing it makes some clients
	// drop the whole event.
	event.End = event.Start.Add(-time.Hour)
	if _, found := property(unfold(Calendar{Events: []Event{event}}.ICS()), "DTEND"); found {
		t.Error("an end before the start was written")
	}
}

// TestAFeedIsWellFormedWhateverWasTyped: every title and body here was typed
// by somebody, and a feed is one angle bracket away from a document a reader
// refuses.
func TestAFeedIsWellFormedWhateverWasTyped(t *testing.T) {
	published := time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC)
	feed := Feed{
		ID:    "https://schmerz.example/feed.xml",
		Self:  "https://schmerz.example/feed.xml",
		Link:  "https://schmerz.example/feed",
		Title: "schMERZ-Reformen & Bündnisse <alle>",
		Entries: []Entry{
			{
				ID:        "https://schmerz.example/updates/1",
				Title:     `Rat vertagt </title><script>alert(1)</script>`,
				Summary:   "Kürzung von 100 Mio. € & mehr",
				Content:   "Erste Zeile\nZweite <b>Zeile</b> ]]> & so weiter",
				Link:      "https://schmerz.example/updates/1?a=1&b=2",
				Author:    "Bündnis Düsseldorf",
				Category:  "Haushalt 2027",
				Published: published,
			},
			{
				ID:        "https://schmerz.example/updates/2",
				Title:     "Älter",
				Published: published.Add(-24 * time.Hour),
				Updated:   published.Add(-12 * time.Hour),
			},
		},
	}

	raw, err := feed.Atom()
	if err != nil {
		t.Fatalf("Atom: %v", err)
	}
	if !bytes.HasPrefix(raw, []byte(`<?xml version="1.0" encoding="UTF-8"?>`)) {
		t.Error("the feed does not begin with an XML declaration")
	}

	// Read back by a parser, which is the only judge of well-formedness.
	var parsed struct {
		XMLName xml.Name
		Title   string `xml:"title"`
		Updated string `xml:"updated"`
		Entries []struct {
			Title   string `xml:"title"`
			Content string `xml:"content"`
			Updated string `xml:"updated"`
			Author  struct {
				Name string `xml:"name"`
			} `xml:"author"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("the feed is not well-formed: %v\n%s", err, raw)
	}

	if parsed.XMLName.Space != "http://www.w3.org/2005/Atom" || parsed.XMLName.Local != "feed" {
		t.Errorf("root element = %+v, want an Atom feed", parsed.XMLName)
	}
	if parsed.Title != feed.Title {
		t.Errorf("title = %q, want it back exactly", parsed.Title)
	}
	if len(parsed.Entries) != 2 {
		t.Fatalf("%d entries, want 2 — markup in a title added or removed one", len(parsed.Entries))
	}
	if parsed.Entries[0].Title != feed.Entries[0].Title {
		t.Errorf("entry title = %q, want the markup back as text", parsed.Entries[0].Title)
	}
	if parsed.Entries[0].Content != feed.Entries[0].Content {
		t.Errorf("content = %q, want it back exactly", parsed.Entries[0].Content)
	}
	if parsed.Entries[0].Author.Name != "Bündnis Düsseldorf" {
		t.Errorf("author = %q", parsed.Entries[0].Author.Name)
	}

	// The feed changed when its newest entry did.
	if parsed.Updated != "2026-10-01T09:30:00Z" {
		t.Errorf("feed updated = %q, want its newest entry's time", parsed.Updated)
	}
	// An entry edited after it was published says so.
	if parsed.Entries[1].Updated != "2026-09-30T21:30:00Z" {
		t.Errorf("entry updated = %q, want the later of published and updated", parsed.Entries[1].Updated)
	}
}

// TestAnEmptyFeedDoesNotChangeOnEveryFetch: stamping it with the current time
// would tell every reader, on every poll, that something is new.
func TestAnEmptyFeedDoesNotChangeOnEveryFetch(t *testing.T) {
	first, err := Feed{ID: "x", Title: "leer"}.Atom()
	if err != nil {
		t.Fatalf("Atom: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	second, _ := Feed{ID: "x", Title: "leer"}.Atom()

	if !bytes.Equal(first, second) {
		t.Error("an empty feed rendered differently a moment later")
	}
	if err := xml.Unmarshal(first, new(struct{})); err != nil {
		t.Errorf("an empty feed is not well-formed: %v", err)
	}
}
