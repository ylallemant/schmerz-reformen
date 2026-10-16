package apiclient

import (
	"context"
	"net/http"
	"time"
)

// SignInDifference is one stored value the deployment says otherwise.
type SignInDifference struct {
	Field     string `json:"field"`
	Stored    string `json:"stored"`
	Effective string `json:"effective"`
}

// SignInGroup is one of the site's static groups, and whether it exists.
type SignInGroup struct {
	Name   string `json:"name"`
	Exists bool   `json:"exists"`
}

// SignInStatus is what the console's sign-in page shows: what setup stored,
// what is used, and whether the directory can hand people a way in.
type SignInStatus struct {
	Provisioned bool `json:"provisioned"`

	AppName             string     `json:"app_name,omitempty"`
	StoredInstance      string     `json:"stored_instance,omitempty"`
	ConsoleURL          string     `json:"console_url,omitempty"`
	StoredClientID      string     `json:"stored_client_id,omitempty"`
	SignInByPlatform    bool       `json:"sign_in_by_platform"`
	DirectoryByPlatform bool       `json:"directory_by_platform"`
	ProvisionedBy       string     `json:"provisioned_by,omitempty"`
	ProvisionedAt       *time.Time `json:"provisioned_at,omitempty"`

	DirectoryAddress    string `json:"directory_address,omitempty"`
	DirectoryAccount    string `json:"directory_account,omitempty"`
	DirectoryFromEnv    bool   `json:"directory_from_environment"`
	DirectoryProblem    string `json:"directory_problem,omitempty"`
	BrandDomain         string `json:"brand_domain,omitempty"`
	BrandIsDefault      bool   `json:"brand_is_default"`
	BrandHasRecovery    bool   `json:"brand_has_recovery"`
	RecoveryFlow        string `json:"recovery_flow,omitempty"`
	RecoveryFlowIsOurs  bool   `json:"recovery_flow_is_ours"`
	RecoveryFlowMissing bool   `json:"recovery_flow_missing"`

	Groups      []SignInGroup      `json:"groups"`
	Differences []SignInDifference `json:"differences"`
}

// SignInStatus reads it. consolePlatform says the console signs in with the
// deployment platform's client, which only the console knows.
func (c *Client) SignInStatus(ctx context.Context, consolePlatform bool) (SignInStatus, error) {
	var status SignInStatus
	path := "/v1/staff/sign-in"
	if consolePlatform {
		path += "?console_platform=true"
	}
	err := c.get(ctx, path, &status)
	return status, err
}

// AlignSignIn brings what setup stored in line with what the deployment
// gives, and returns the page as it is afterwards.
func (c *Client) AlignSignIn(ctx context.Context, consolePlatform bool) (SignInStatus, error) {
	var status SignInStatus
	err := c.write(ctx, http.MethodPut, "/v1/staff/sign-in",
		map[string]any{"console_platform": consolePlatform}, &status)
	return status, err
}
