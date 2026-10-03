package models

import (
	"slices"
	"strings"
	"time"
)

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
// changed once, for every list it appears in. That is also why nobody changes
// it alone — see OrganisationChange.
type Organisation struct {
	Model
	OrganisationValues
}

// OrganisationValues is everything about an organisation that a change can
// propose: the organisation itself carries one set, and a change carries the
// set before and the set after.
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

// ChangeKind is what a change does to an organisation.
type ChangeKind string

const (
	ChangeCreate ChangeKind = "create"
	ChangeUpdate ChangeKind = "update"
	ChangeDelete ChangeKind = "delete"
)

// ChangeStatus is where a change stands.
type ChangeStatus string

const (
	// ChangePending waits for votes. The organisation is untouched meanwhile.
	ChangePending ChangeStatus = "pending"

	// ChangeApplied reached its approvals and was carried out.
	ChangeApplied ChangeStatus = "applied"

	// ChangeRejected reached as many rejections as it needed approvals.
	ChangeRejected ChangeStatus = "rejected"

	// ChangeWithdrawn was taken back by its author.
	ChangeWithdrawn ChangeStatus = "withdrawn"

	// ChangeFailed was approved and could not be carried out: the parent it
	// named was deleted meanwhile, or the organisation it would delete is now
	// in a collective. Its Reason says which.
	ChangeFailed ChangeStatus = "failed"
)

// Open reports whether the change still takes votes.
func (s ChangeStatus) Open() bool { return s == ChangePending }

// The fields a change can set, as Fields lists them.
const (
	FieldName    = "name"
	FieldKind    = "kind"
	FieldWebsite = "website"
	FieldParent  = "parent"
	FieldLogo    = "logo"
	FieldPlace   = "place"
)

// ChangeFields lists them in the order a change is shown in.
var ChangeFields = []string{FieldName, FieldKind, FieldWebsite, FieldParent, FieldPlace, FieldLogo}

// OrganisationChange is a proposed creation, change or deletion of an
// organisation.
//
// **Nobody changes an organisation alone.** It appears in the member list of
// every collective it belongs to, so a change is seen by alliances its author
// does not edit for — a logo replaced, a name made a slur, a union deleted out
// of five lists at once. So every change is a proposal, and takes effect only
// once enough editors other than its author approve it. Until then the
// organisation stays exactly as it was: what readers see is always something
// several people agreed to.
//
// An update carries only the fields it changes (Fields), and is applied
// field by field. Two pending changes to different fields of one organisation
// therefore cannot undo each other, whichever is approved first; two to the
// same field are refused when the second is proposed.
type OrganisationChange struct {
	Model

	// OrganisationID is the organisation it is about. For a creation it is
	// assigned when the change is proposed, so the organisation it creates
	// has that identifier from the start.
	OrganisationID string `gorm:"index;size:36" json:"organisation_id"`

	Kind   ChangeKind   `gorm:"size:16" json:"kind"`
	Status ChangeStatus `gorm:"index;size:16" json:"status"`

	// Fields lists what an update sets, comma-separated, from ChangeFields.
	// A creation sets everything and a deletion nothing.
	Fields string `gorm:"size:128" json:"fields,omitempty"`

	// Before is what the organisation said when the change was proposed, so
	// the people asked to approve it see what it replaces. After is what it
	// proposes.
	Before OrganisationValues `gorm:"embedded;embeddedPrefix:before_" json:"before"`
	After  OrganisationValues `gorm:"embedded;embeddedPrefix:after_" json:"after"`

	// Author is who proposed it: their OIDC subject, which decides that they
	// cannot approve it, and their name at the time.
	Author     string `gorm:"index;size:256" json:"author"`
	AuthorName string `gorm:"size:256" json:"author_name,omitempty"`

	// DecidedAt is when it stopped being pending.
	DecidedAt *time.Time `json:"decided_at,omitempty"`

	// Reason is why an approved change could not be carried out.
	Reason string `gorm:"size:256" json:"reason,omitempty"`

	Votes []ChangeVote `gorm:"foreignKey:ChangeID;constraint:OnDelete:CASCADE" json:"votes,omitempty"`
}

// FieldList is Fields as a list.
func (c OrganisationChange) FieldList() []string {
	if c.Fields == "" {
		return nil
	}
	return strings.Split(c.Fields, ",")
}

// Sets reports whether the change sets a field.
func (c OrganisationChange) Sets(field string) bool {
	return slices.Contains(c.FieldList(), field)
}

// Tally counts the votes for and against.
func (c OrganisationChange) Tally() (approvals, rejections int) {
	for _, vote := range c.Votes {
		if vote.Approve {
			approvals++
		} else {
			rejections++
		}
	}
	return approvals, rejections
}

// ChangeVote is one editor's approval or rejection of a change.
//
// One per editor and change, and final: a vote that could be taken back would
// let one person approve, see it applied, and approve the next with the same
// hand. The author of a change has none.
type ChangeVote struct {
	Model

	ChangeID string `gorm:"uniqueIndex:idx_change_voter;size:36" json:"change_id"`

	// Voter is the editor's OIDC subject; VoterName what they were called.
	Voter     string `gorm:"uniqueIndex:idx_change_voter;size:256" json:"voter"`
	VoterName string `gorm:"size:256" json:"voter_name,omitempty"`

	Approve bool `json:"approve"`

	// Comment is why. Required for a rejection — "no" with no reason gives the
	// author nothing to fix — and optional for an approval.
	Comment string `gorm:"size:512" json:"comment,omitempty"`
}
