package models

// FollowTarget is what a reader can follow.
type FollowTarget string

const (
	FollowCollective FollowTarget = "collective"
	FollowTopic      FollowTarget = "topic"
)

// Valid reports whether the target is one the model defines.
func (t FollowTarget) Valid() bool {
	return t == FollowCollective || t == FollowTopic
}

// Follow is a reader asking to be told about a collective or a topic.
//
// # What this row is, and what it is not
//
// It pairs an account with a political alliance, which sounds like exactly the
// list nobody should keep. Two things make it defensible, and both are
// load-bearing.
//
// The account is nobody: a random handle and a passkey, with no address, no
// phone number and no name anybody verified. The row says that *some* device
// follows the Düsseldorf alliance, and a database in the wrong hands cannot
// turn that into a person.
//
// And the reader asked for it, sees the whole list on their own page, and can
// empty it. It is a subscription they hold, not a record kept about them.
//
// What is public is the **count** and never the list: a collective learns that
// four hundred people follow it, and not which.
type Follow struct {
	Model

	AccountID string `gorm:"size:36;uniqueIndex:idx_follow" json:"-"`

	TargetType FollowTarget `gorm:"size:16;uniqueIndex:idx_follow;index:idx_follow_target" json:"target_type"`
	TargetID   string       `gorm:"size:36;uniqueIndex:idx_follow;index:idx_follow_target" json:"target_id"`
}

// Participation is a reader saying they intend to come to an action.
//
// An intent, not a registration: nobody is checked at the door and nobody is
// counted on arrival. Its worth is the number — an organiser deciding whether
// to book the bigger hall, and a reader deciding whether to go alone — and,
// for the reader, being told if the action moves or is called off.
//
// The same terms as a Follow: an anonymous account, a public count, a private
// list.
type Participation struct {
	Model

	AccountID string `gorm:"size:36;uniqueIndex:idx_participation" json:"-"`
	ActionID  string `gorm:"size:36;uniqueIndex:idx_participation;index" json:"action_id"`
}
