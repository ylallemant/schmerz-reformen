package config

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// KeyOrganisationApprovals is how many editors other than its author must
// approve a change to an organisation before it is applied — and how many
// rejections close it.
const KeyOrganisationApprovals = "organisation-approvals"

// DefaultOrganisationApprovals is three: one is a single person's say, two is
// a friend's, and three is the smallest number that is a group agreeing.
const DefaultOrganisationApprovals = 3

// RegisterCurationFlags declares how organisations are curated. The backend's
// alone: it is the one that counts the votes.
func RegisterCurationFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().Int(KeyOrganisationApprovals, DefaultOrganisationApprovals,
		"approvals by editors other than its author that a change to an organisation needs; as many rejections close it")
}

// LoadOrganisationApprovals reads the threshold, refusing one that would let
// a change through with nobody's agreement.
func LoadOrganisationApprovals() (int, error) {
	approvals := viper.GetInt(KeyOrganisationApprovals)
	if approvals < 1 {
		return 0, fmt.Errorf("--%s must be at least 1, got %d", KeyOrganisationApprovals, approvals)
	}
	return approvals, nil
}
