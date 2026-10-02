package syndication

import (
	"bytes"
	"encoding/xml"
	"time"
)

// Entry is one item of a feed.
type Entry struct {
	// ID identifies the entry for ever: its own address. A reader uses it to
	// know what it has already shown.
	ID string

	Title string

	// Summary is the short form and Content the whole text. Both are plain
	// text: nothing here is HTML, and declaring it as text is what makes a
	// reader escape it rather than render it.
	Summary string
	Content string

	// Link is the entry's page.
	Link string

	// Author is who published it — a collective, never a person.
	Author string

	// Category is what it is about: the topic.
	Category string

	Published time.Time
	Updated   time.Time
}

// Feed is a list of entries with a name.
type Feed struct {
	// ID is the feed's own address, and Self the address it is fetched from.
	ID   string
	Self string

	// Link is the page the feed is the machine-readable form of.
	Link string

	Title    string
	Subtitle string

	Entries []Entry
}

// The Atom document, as encoding/xml writes it. Kept private: the shape of
// the wire format is this file's business, and callers describe a feed in
// their own terms.
type atomFeed struct {
	XMLName  xml.Name    `xml:"http://www.w3.org/2005/Atom feed"`
	Title    string      `xml:"title"`
	Subtitle string      `xml:"subtitle,omitempty"`
	ID       string      `xml:"id"`
	Updated  string      `xml:"updated"`
	Links    []atomLink  `xml:"link"`
	Entries  []atomEntry `xml:"entry"`
}

type atomLink struct {
	Rel  string `xml:"rel,attr,omitempty"`
	Type string `xml:"type,attr,omitempty"`
	Href string `xml:"href,attr"`
}

type atomText struct {
	Type string `xml:"type,attr"`
	Body string `xml:",chardata"`
}

type atomEntry struct {
	Title     string        `xml:"title"`
	ID        string        `xml:"id"`
	Links     []atomLink    `xml:"link"`
	Published string        `xml:"published,omitempty"`
	Updated   string        `xml:"updated"`
	Author    *atomAuthor   `xml:"author,omitempty"`
	Category  *atomCategory `xml:"category,omitempty"`
	Summary   *atomText     `xml:"summary,omitempty"`
	Content   *atomText     `xml:"content,omitempty"`
}

type atomAuthor struct {
	Name string `xml:"name"`
}

type atomCategory struct {
	Term string `xml:"term,attr"`
}

// Atom renders the feed as an Atom 1.0 (RFC 4287) document.
//
// The escaping is encoding/xml's, which is the point of building a value and
// marshalling it rather than writing tags: every title, name and body here
// was typed by somebody, and a feed assembled by concatenation would be one
// angle bracket away from a document a reader refuses.
func (f Feed) Atom() ([]byte, error) {
	document := atomFeed{
		Title:    f.Title,
		Subtitle: f.Subtitle,
		ID:       f.ID,
		Updated:  rfc3339(f.updated()),
	}
	if f.Self != "" {
		document.Links = append(document.Links,
			atomLink{Rel: "self", Type: "application/atom+xml", Href: f.Self})
	}
	if f.Link != "" {
		document.Links = append(document.Links,
			atomLink{Rel: "alternate", Type: "text/html", Href: f.Link})
	}

	for _, entry := range f.Entries {
		item := atomEntry{
			Title:   entry.Title,
			ID:      entry.ID,
			Updated: rfc3339(latest(entry.Updated, entry.Published)),
		}
		if entry.Link != "" {
			item.Links = []atomLink{{Rel: "alternate", Type: "text/html", Href: entry.Link}}
		}
		if !entry.Published.IsZero() {
			item.Published = rfc3339(entry.Published)
		}
		if entry.Author != "" {
			// An entry must have an author somewhere, and the feed as a whole
			// has none: it is many collectives' news, each entry its own.
			item.Author = &atomAuthor{Name: entry.Author}
		}
		if entry.Category != "" {
			item.Category = &atomCategory{Term: entry.Category}
		}
		if entry.Summary != "" {
			item.Summary = &atomText{Type: "text", Body: entry.Summary}
		}
		if entry.Content != "" {
			item.Content = &atomText{Type: "text", Body: entry.Content}
		}
		document.Entries = append(document.Entries, item)
	}

	var b bytes.Buffer
	b.WriteString(xml.Header)

	encoder := xml.NewEncoder(&b)
	encoder.Indent("", "  ")
	if err := encoder.Encode(document); err != nil {
		return nil, err
	}
	b.WriteByte('\n')
	return b.Bytes(), nil
}

// updated is when the feed last changed: its newest entry, or — for a feed
// with nothing in it yet — the Unix epoch, which is a valid date and an
// honest "never". The current time would tell every reader the empty feed had
// changed on every fetch.
func (f Feed) updated() time.Time {
	var newest time.Time
	for _, entry := range f.Entries {
		if moment := latest(entry.Updated, entry.Published); moment.After(newest) {
			newest = moment
		}
	}
	if newest.IsZero() {
		return time.Unix(0, 0)
	}
	return newest
}

func latest(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }
