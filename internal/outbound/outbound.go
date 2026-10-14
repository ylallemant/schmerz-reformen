// Package outbound is how the backend presents itself to the services it
// calls: Nominatim and Geoapify, the operator's Authentik, and the push
// services of readers' browsers.
//
// # Every request names this site, and how to reach whoever runs it
//
// Go's default User-Agent, "Go-http-client/1.1", is anonymous and a known bot
// signature: WAF rules of the CrowdSec kind ban it outright, and a service
// that is being hammered has nobody to write to. Nominatim's usage policy
// requires an identifying one and blocks requests without it. So every
// outbound request carries
//
//	schmerz-reformen/<version> (+<project>; <contact>)
//
// — what is asking, where its code is, and an address that answers.
//
// One client for all of them, rather than a header set in each: the request
// somebody adds next is the one that would otherwise go out anonymous.
package outbound

import (
	"net/http"
	"strings"
	"time"
)

// Product is the name every request is made under.
const Product = "schmerz-reformen"

// UserAgent renders the identification. version is the binary's; project and
// contact are left out when empty rather than rendered as empty parentheses.
func UserAgent(version, project, contact string) string {
	if version == "" {
		version = "dev"
	}
	var details []string
	if project = strings.TrimSpace(project); project != "" {
		details = append(details, "+"+project)
	}
	if contact = strings.TrimSpace(contact); contact != "" {
		details = append(details, contact)
	}
	agent := Product + "/" + version
	if len(details) > 0 {
		agent += " (" + strings.Join(details, "; ") + ")"
	}
	return agent
}

// Client returns an HTTP client that sends userAgent on every request which
// does not set one of its own.
func Client(timeout time.Duration, userAgent string) *http.Client {
	return &http.Client{Timeout: timeout, Transport: Transport(nil, userAgent)}
}

// Transport wraps base (the default transport when nil) so that every request
// carries userAgent. A request that already names a User-Agent keeps it.
func Transport(base http.RoundTripper, userAgent string) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &agentTransport{base: base, userAgent: userAgent}
}

type agentTransport struct {
	base      http.RoundTripper
	userAgent string
}

func (t *agentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.userAgent == "" || req.Header.Get("User-Agent") != "" {
		return t.base.RoundTrip(req)
	}
	// A RoundTripper must not change the request it was given.
	stamped := req.Clone(req.Context())
	stamped.Header.Set("User-Agent", t.userAgent)
	return t.base.RoundTrip(stamped)
}
