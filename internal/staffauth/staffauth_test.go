package staffauth

import (
	"errors"
	"testing"
)

// TestIdentityRoundTrip: a group in somebody's identity provider can be called
// anything, including things an HTTP header cannot carry as they are.
func TestIdentityRoundTrip(t *testing.T) {
	identity := Identity{
		Subject: "a1b2c3",
		Name:    "Çağla Müller-Özdemir",
		Groups:  []string{"Bündnis Düsseldorf, Redaktion", "schmerz-admins", "line\nbreak"},
	}
	encoded, err := identity.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	for _, r := range encoded {
		if r > 126 || r < 33 {
			t.Fatalf("the header value contains %q, which a header cannot carry", r)
		}
	}

	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.Subject != identity.Subject || decoded.Name != identity.Name || len(decoded.Groups) != 3 ||
		decoded.Groups[0] != identity.Groups[0] || decoded.Groups[2] != identity.Groups[2] {
		t.Errorf("decoded %+v, want %+v", decoded, identity)
	}
}

// TestAnIdentityWithNobodyInItIsRefused: every write through the console is
// recorded against a subject, so an identity without one cannot be acted on.
func TestAnIdentityWithNobodyInItIsRefused(t *testing.T) {
	for name, identity := range map[string]Identity{
		"no subject":         {Name: "Somebody", Groups: []string{"schmerz-admins"}},
		"a subject of space": {Subject: "   "},
	} {
		encoded, err := identity.Encode()
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		if _, err := Decode(encoded); !errors.Is(err, ErrNoSubject) {
			t.Errorf("%s: err = %v, want ErrNoSubject", name, err)
		}
	}

	for _, garbage := range []string{"", "{not json}", "bm90IGpzb24"} {
		if _, err := Decode(garbage); err == nil {
			t.Errorf("Decode(%q) was accepted", garbage)
		}
	}
}
