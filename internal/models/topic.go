package models

import "time"

// TopicKind says what is being done to people.
type TopicKind string

const (
	// TopicCut is money taken out of a budget.
	TopicCut TopicKind = "cut"
	// TopicReform is a change of rules: pensions, the Bürgergeld, labour law.
	TopicReform TopicKind = "reform"
	// TopicClosure is a place that stops existing: a pool, a library, a ward.
	TopicClosure TopicKind = "closure"
	// TopicPrivatisation is something public being sold or handed over.
	TopicPrivatisation TopicKind = "privatisation"
	TopicOther         TopicKind = "other"
)

// TopicKinds lists them in the order a form and a filter offer them.
var TopicKinds = []TopicKind{
	TopicCut, TopicReform, TopicClosure, TopicPrivatisation, TopicOther,
}

// Valid reports whether the kind is one the model defines.
func (k TopicKind) Valid() bool {
	for _, known := range TopicKinds {
		if k == known {
			return true
		}
	}
	return false
}

// Level is which government is doing it.
//
// It is the reason the map matters. A federal reform is announced in Berlin
// and lands as a line in a city budget months later, and the people affected
// meet it at the third level down — so a topic says where the decision is
// taken, and its pin says where it is felt.
type Level string

const (
	LevelFederal   Level = "federal"   // Bund
	LevelState     Level = "state"     // Land
	LevelMunicipal Level = "municipal" // Kommune
)

// Levels lists them from the top down.
var Levels = []Level{LevelFederal, LevelState, LevelMunicipal}

// Valid reports whether the level is one the model defines.
func (l Level) Valid() bool {
	for _, known := range Levels {
		if l == known {
			return true
		}
	}
	return false
}

// Topic is one thing a collective is campaigning about: a cut, a reform, a
// closure.
//
// It is a standing page rather than a news item. The news goes in its updates;
// the topic itself says what is planned, by whom, how much and where, and
// stays correct for as long as the fight lasts.
type Topic struct {
	Model

	CollectiveID string `gorm:"index;size:36" json:"collective_id"`

	Kind  TopicKind `gorm:"index;size:16" json:"kind"`
	Level Level     `gorm:"index;size:16" json:"level"`

	Title string `gorm:"size:200" json:"title"`

	// Summary is what a card and a map popup show; Body is the page.
	Summary string `gorm:"size:500" json:"summary,omitempty"`
	Body    string `gorm:"type:text" json:"body,omitempty"`

	// Amount is the money at stake, in whole euros. Zero means no figure was
	// given, which is the honest answer for most reforms — a number invented
	// to fill a field would be the first thing an opponent quotes back.
	//
	// Whole euros rather than cents: these are budget lines, and the hundred
	// million cut from a city's budget is not improved by two decimal places.
	Amount int64 `json:"amount,omitempty"`

	// SourceURL is where the plan itself can be read: the council document,
	// the draft bill, the press report. A claim about a budget with no source
	// is a rumour.
	SourceURL string `gorm:"size:512" json:"source_url,omitempty"`

	// Location is where it lands. Optional: a federal reform with no local
	// angle has no pin and simply stays off the map.
	Location Location `gorm:"embedded;embeddedPrefix:location_" json:"location"`

	Status PublishStatus `gorm:"index;size:16" json:"status"`

	// PublishedAt is when it first became public. It orders listings, and it
	// is set once: editing a published topic must not send it back to the top
	// of everybody's page.
	PublishedAt *time.Time `gorm:"index" json:"published_at,omitempty"`
}

// TopicUpdate is one piece of news about a topic — the feed is made of these.
//
// "The council postponed the vote", "the cut was reduced to sixty million",
// "three hundred people at the town hall". It always belongs to a topic,
// because news with nothing to be news about is a blog, and a blog is not what
// somebody who followed "the Düsseldorf budget" asked to be told about.
type TopicUpdate struct {
	Model

	TopicID string `gorm:"index;size:36" json:"topic_id"`

	// CollectiveID repeats the topic's own. It is what the permission check
	// and the "everything this collective said" listing read, and looking it
	// up through the topic every time would make the hottest query here a
	// join.
	CollectiveID string `gorm:"index;size:36" json:"collective_id"`

	Title string `gorm:"size:200" json:"title"`
	Body  string `gorm:"type:text" json:"body"`

	SourceURL string `gorm:"size:512" json:"source_url,omitempty"`

	Status      PublishStatus `gorm:"index;size:16" json:"status"`
	PublishedAt *time.Time    `gorm:"index" json:"published_at,omitempty"`
}
