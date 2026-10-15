package authentik

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// A brand is how Authentik picks what answers a request: by the host it
// arrives on, falling back to the brand marked default. A recovery link is
// made with the recovery flow of the brand that answered the call that asked
// for it, and built on that call's host.
//
// So the brand that matters to this site is the one its own address gets:
//
//   - on a shared address — auth.codersyndicate.cloud — the default brand,
//     whose recovery flow is whichever project bound one first. This code
//     binds its own there only when there is none;
//   - on an address of its own — auth.schmerz-reformen.de, which the platform
//     gives a project with a brand of its own and deliberately no recovery
//     flow ("a project binds its own") — that brand, which nobody else's
//     links go through.

// brandInfo is what this package reads of a brand.
type brandInfo struct {
	UUID         string `json:"brand_uuid"`
	Domain       string `json:"domain"`
	Default      bool   `json:"default"`
	RecoveryFlow string `json:"flow_recovery"`
}

// ownBrand is the brand this client's address is answered by: the one whose
// domain is exactly the host, or else the default. ok is false when there is
// neither.
func (c *Client) ownBrand(ctx context.Context) (brandInfo, bool, error) {
	host := c.host()
	var exact page[brandInfo]
	if err := c.do(ctx, http.MethodGet, "/core/brands/?domain="+url.QueryEscape(host), nil, &exact); err != nil {
		return brandInfo{}, false, fmt.Errorf("read the brand on %s: %w", host, err)
	}
	for _, brand := range exact.Results {
		if strings.EqualFold(brand.Domain, host) {
			return brand, true, nil
		}
	}

	var defaults page[brandInfo]
	if err := c.do(ctx, http.MethodGet, "/core/brands/?default=true", nil, &defaults); err != nil {
		return brandInfo{}, false, fmt.Errorf("read the default brand: %w", err)
	}
	for _, brand := range defaults.Results {
		if brand.Default {
			return brand, true, nil
		}
	}
	return brandInfo{}, false, nil
}

// host is the host this client talks to.
func (c *Client) host() string {
	parsed, err := url.Parse(c.baseURL)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}
