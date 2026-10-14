package models

import (
	"errors"
	"regexp"
	"slices"
	"strings"
)

// Onboarding lets somebody whose address is at one of a few chosen domains —
// the movement's own, sozialratschlag.de say — make their own console account
// rather than waiting for an administrator to make it.
//
// # Who they are is not proven, so what they get is held
//
// Nothing is sent to the address: this project runs no mail server, and the
// way in is shown to whoever typed it. So the address is a claim, and anybody
// could make an account under somebody else's. That is why the account goes
// into the waiting group and nowhere else: it can set up its passkey at once,
// it can do nothing until a site administrator has looked at it, and an
// administrator who does not recognise it turns it away.

// PendingGroupSuffix completes the group of accounts waiting for approval.
const PendingGroupSuffix = "-pending"

// PendingGroupName is the group self-made accounts wait in. It is not a role:
// being in it lets nobody into the console.
func PendingGroupName(app string) string { return app + PendingGroupSuffix }

// ErrDomain is a domain that cannot be one.
var ErrDomain = errors.New("not a domain")

// domainPattern is a host name: labels of letters, digits and hyphens, at
// least two of them. Shallow on purpose — it refuses what is plainly not a
// domain, and anything that could reach a page as more than text.
var domainPattern = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// ParseDomains reads a list of domains as an administrator types it: one per
// line, or separated by commas or spaces, with or without a leading "@".
// Lowercased, deduplicated, in the order given.
func ParseDomains(input string) ([]string, error) {
	fields := strings.FieldsFunc(input, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\r' || r == '\t'
	})
	domains := []string{}
	for _, field := range fields {
		domain := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(field), "@"))
		if domain == "" {
			continue
		}
		if !domainPattern.MatchString(domain) {
			return nil, errors.Join(ErrDomain, errors.New(field))
		}
		if !slices.Contains(domains, domain) {
			domains = append(domains, domain)
		}
	}
	return domains, nil
}

// JoinDomains is how a list is stored and shown.
func JoinDomains(domains []string) string { return strings.Join(domains, ", ") }

// OnboardingDomainList is the stored list, read back.
func (a AuthSettings) OnboardingDomainList() []string {
	domains, err := ParseDomains(a.OnboardingDomains)
	if err != nil {
		// Stored by this code, so only a hand-edited row lands here; nobody
		// onboards from it rather than everybody.
		return []string{}
	}
	return domains
}

// MayOnboard reports whether an address is at one of the domains. The whole
// domain must match: a list naming sozialratschlag.de admits neither
// mail.sozialratschlag.de nor sozialratschlag.de.example.
func MayOnboard(email string, domains []string) bool {
	at := strings.LastIndex(email, "@")
	if at < 1 || at == len(email)-1 {
		return false
	}
	return slices.Contains(domains, strings.ToLower(email[at+1:]))
}
