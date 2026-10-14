package apiclient

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// Somebody at one of the onboarding domains makes their own account, which
// waits for a site administrator. See the backend's onboarding.go.

// Onboard makes an account for an address at an onboarding domain, and
// returns its way in, once. Needs a client made with AsConsole: it is called
// before anybody is signed in.
func (c *Client) Onboard(ctx context.Context, email, name string) (WayIn, error) {
	var link WayIn
	err := c.write(ctx, http.MethodPost, "/v1/console/onboarding", map[string]any{
		"email": email, "name": name,
	}, &link)
	return link, err
}

// ApprovePerson lets in somebody who made their own account.
func (c *Client) ApprovePerson(ctx context.Context, pk int, username string) error {
	return c.send(ctx, http.MethodPut, approvalPath(pk, username), "", nil)
}

// RefusePerson turns them away; their account is deactivated.
func (c *Client) RefusePerson(ctx context.Context, pk int, username string) error {
	return c.send(ctx, http.MethodDelete, approvalPath(pk, username), "", nil)
}

func approvalPath(pk int, username string) string {
	return "/v1/staff/people/" + strconv.Itoa(pk) + "/approval?username=" + url.QueryEscape(username)
}

// SetOnboardingDomains replaces the list, as typed, and returns it as stored.
func (c *Client) SetOnboardingDomains(ctx context.Context, domains string) ([]string, error) {
	var answer struct {
		Domains []string `json:"domains"`
	}
	err := c.write(ctx, http.MethodPut, "/v1/staff/onboarding", map[string]any{"domains": domains}, &answer)
	return answer.Domains, err
}
