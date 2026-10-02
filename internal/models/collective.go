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

	// AuthGroup is the identity-provider group whose members may manage this
	// collective's content.
	//
	// It is the whole of the permission model: there is no table of editors
	// here, no invitations and no roles of our own. A collective adds and
	// removes its content creators in the identity provider, where the people
	// already are, and this site reads the answer at sign-in.
	//
	// Never serialised to the public API: the name of a group in somebody's
	// identity provider is nobody else's business.
	AuthGroup string `gorm:"index;size:128" json:"-"`

	// LogoID is the uploaded logo, if there is one.
	LogoID string `gorm:"size:36" json:"logo_id,omitempty"`

	Status PublishStatus `gorm:"index;size:16" json:"status"`

	Members []CollectiveMember `gorm:"constraint:OnDelete:CASCADE" json:"members,omitempty"`
}

// MemberKind says what sort of organisation a member is.
type MemberKind string

const (
	MemberUnion       MemberKind = "union"
	MemberParty       MemberKind = "party"
	MemberAssociation MemberKind = "association"
	MemberInitiative  MemberKind = "initiative"
	MemberOther       MemberKind = "other"
)

// MemberKinds lists them in the order a form offers them.
var MemberKinds = []MemberKind{
	MemberUnion, MemberParty, MemberAssociation, MemberInitiative, MemberOther,
}

// Valid reports whether the kind is one the model defines.
func (k MemberKind) Valid() bool {
	for _, known := range MemberKinds {
		if k == known {
			return true
		}
	}
	return false
}

// CollectiveMember is one organisation inside a collective.
//
// **An organisation, never a person.** This is the list a reader looks at to
// decide whether a collective is who it says it is — "ver.di, the tenants'
// association and two parties" — so it carries a name, a website and a logo,
// and nothing that could identify an individual.
type CollectiveMember struct {
	Model

	CollectiveID string `gorm:"index;size:36" json:"collective_id"`

	Name    string     `gorm:"size:160" json:"name"`
	Kind    MemberKind `gorm:"size:16" json:"kind"`
	Website string     `gorm:"size:512" json:"website,omitempty"`

	// LogoID is the uploaded logo, if there is one.
	LogoID string `gorm:"size:36" json:"logo_id,omitempty"`

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
