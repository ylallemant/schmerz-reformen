package models

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Collective is an alliance: unions, opposition parties, associations and
// initiatives that decided to speak together in one place.
//
// It is the unit everything else hangs from. A topic, an update and an action
// each belong to exactly one collective, and that is also the unit of
// permission: whoever may edit the collective may edit what it published.
type Collective struct {
	Model

	Name string `gorm:"size:160" json:"name"`

	// Slug is the collective's address — /collectives/{slug} — and is unique.
	// It is chosen once and should not change afterwards: it is in every link
	// somebody printed on a flyer.
	Slug string `gorm:"uniqueIndex;size:96" json:"slug"`

	// Summary is one or two sentences for a card; Description is the page.
	Summary     string `gorm:"size:400" json:"summary,omitempty"`
	Description string `gorm:"type:text" json:"description,omitempty"`

	Website string `gorm:"size:512" json:"website,omitempty"`

	// Contact is how the public reaches the collective — an address or a URL
	// it chose to publish. It belongs to an organisation, not to a person, and
	// it is the only contact detail this site holds about anybody.
	Contact string `gorm:"size:256" json:"contact,omitempty"`

	// Location is where the collective is active: the city whose budget it is
	// fighting over. Optional — a national alliance has no pin.
	Location Location `gorm:"embedded;embeddedPrefix:location_" json:"location"`

	// AdminGroup and AuthorGroup are the identity-provider groups of the
	// collective's administrators — who edit its profile, choose its member
	// organisations and its authors — and of its authors, who publish its
	// topics, news and actions.
	//
	// They are the whole of the permission model: there is no table of
	// editors here. Named from the slug when the collective is created and
	// stored, so changing the slug later never orphans them; created in the
	// directory by the backend.
	//
	// Never serialised to the public API: the name of a group in somebody's
	// identity provider is nobody else's business.
	AdminGroup  string `gorm:"index;size:160" json:"-"`
	AuthorGroup string `gorm:"index;size:160" json:"-"`

	// LogoID is the uploaded logo, if there is one.
	LogoID string `gorm:"size:36" json:"logo_id,omitempty"`

	Status PublishStatus `gorm:"index;size:16" json:"status"`

	Members []CollectiveMember `gorm:"constraint:OnDelete:CASCADE" json:"members,omitempty"`
}

// CollectiveMember is one organisation's place in a collective's list.
//
// The organisation itself — its name, logo, place — belongs to no collective
// and is changed only by agreement (see Organisation). What is the
// collective's own is that it counts the organisation among its members, and
// in which order.
type CollectiveMember struct {
	Model

	CollectiveID   string `gorm:"index;size:36" json:"collective_id"`
	OrganisationID string `gorm:"index;size:36" json:"organisation_id"`

	// Organisation is loaded with the member, never written through it.
	Organisation Organisation `gorm:"foreignKey:OrganisationID" json:"organisation"`

	// Position orders the list. The collective decides who is named first,
	// and that is a political decision this site has no business making
	// alphabetically on its behalf.
	Position int `gorm:"index" json:"position"`
}

// Slugify turns a name into the form an address uses: lower-case ASCII letters
// and digits, separated by single hyphens.
//
// German is folded rather than dropped — "Düsseldorf" becomes "duesseldorf",
// not "dsseldorf" — because the people typing these addresses know how their
// own city is spelled without an umlaut.
func Slugify(value string) string {
	folded := strings.NewReplacer(
		"ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss",
		"Ä", "ae", "Ö", "oe", "Ü", "ue", "ẞ", "ss",
	).Replace(value)

	var b strings.Builder
	hyphen := true // suppresses a leading hyphen
	for _, r := range norm.NFD.String(folded) {
		switch {
		case unicode.Is(unicode.Mn, r):
			// A combining mark left behind by decomposition: "é" is "e" here.
			continue
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(unicode.ToLower(r))
			hyphen = false
		default:
			if !hyphen {
				b.WriteByte('-')
				hyphen = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}
