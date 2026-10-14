package models

import (
	"errors"
	"slices"
	"testing"
)

// TestDomainsAreReadAsAnAdministratorTypesThem.
func TestDomainsAreReadAsAnAdministratorTypesThem(t *testing.T) {
	got, err := ParseDomains(" @Sozialratschlag.DE,\nverdi.de  verdi.de; ")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"sozialratschlag.de", "verdi.de"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, err := ParseDomains("   "); err != nil || len(got) != 0 {
		t.Errorf("nothing typed = %q, %v; want an empty list", got, err)
	}

	// Anything that is not plainly a domain is refused, not cleaned: it is
	// shown on a page anybody can reach.
	for _, bad := range []string{"anna@sozialratschlag.de", "<script>", "localhost", "sozial ratschlag.de;x\"y", "-a.de", "a..de"} {
		if _, err := ParseDomains(bad); !errors.Is(err, ErrDomain) {
			t.Errorf("%q was accepted as a domain", bad)
		}
	}
}

// TestOnlyTheWholeDomainAdmits: a list naming sozialratschlag.de admits
// neither a subdomain nor a domain that merely ends with it.
func TestOnlyTheWholeDomainAdmits(t *testing.T) {
	domains := []string{"sozialratschlag.de"}
	for email, want := range map[string]bool{
		"anna@sozialratschlag.de":         true,
		"Anna@SOZIALRATSCHLAG.de":         true,
		"anna@mail.sozialratschlag.de":    false,
		"anna@sozialratschlag.de.example": false,
		"anna@evil-sozialratschlag.de":    false,
		"sozialratschlag.de":              false,
		"@sozialratschlag.de":             false,
		"anna@":                           false,
	} {
		if got := MayOnboard(email, domains); got != want {
			t.Errorf("MayOnboard(%q) = %v, want %v", email, got, want)
		}
	}
	if MayOnboard("anna@sozialratschlag.de", nil) {
		t.Error("an empty list admitted somebody")
	}
}
