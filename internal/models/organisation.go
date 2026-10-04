package models

import "slices"

// Organisation is a union, a party, an association or an initiative: one of
// the bodies collectives are made of.
//
// **An organisation, never a person.** It carries a name, a website, a logo
// and a place — the office, the town it is active in — and nothing that could
// identify an individual.
//
// It belongs to no collective. One local branch of a union sits in the
// alliance against the city's cuts and in the one against the hospital
// closure, and it is the same branch in both: its logo and its name are
// changed once, for every list it appears in — by its own administrators, the
// people in its AdminGroup.
type Organisation struct {
	Model
	OrganisationValues

	// Slug names the organisation in its groups, and is fixed when it is
	// created: renaming the organisation must not orphan the groups people
	// were put in.
	Slug string `gorm:"uniqueIndex;size:96" json:"slug"`

	// AdminGroup and MemberGroup are the identity-provider groups of its
	// administrators — who edit it and add its people — and of its people.
	// Stored, not recomputed, for the same reason as the slug.
	AdminGroup  string `gorm:"size:160" json:"admin_group,omitempty"`
	MemberGroup string `gorm:"size:160" json:"member_group,omitempty"`
}

// OrganisationValues is everything about an organisation an administrator
// edits.
type OrganisationValues struct {
	Name    string     `gorm:"size:160" json:"name"`
	Kind    MemberKind `gorm:"size:16" json:"kind"`
	Website string     `gorm:"size:512" json:"website,omitempty"`

	// ParentID is the organisation this one is part of: the district of a
	// union, the federal party of a local branch. Empty for one that stands on
	// its own. A chain of parents never loops — that is checked when a change
	// is proposed and again when it is applied.
	ParentID string `gorm:"index;size:36" json:"parent_id,omitempty"`

	// LogoID is the uploaded logo, if there is one.
	LogoID string `gorm:"size:36" json:"logo_id,omitempty"`

	// Location is where the organisation is: its office, or the town it is
	// active in. Optional, and stored exactly as pinned — an organisation's
	// published address locates nobody.
	Location Location `gorm:"embedded;embeddedPrefix:location_" json:"location"`
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
	return slices.Contains(MemberKinds, k)
}
