// Package staffauth is how the console tells the backend who is acting.
//
// It is the wire format and nothing else: two header names and the identity
// that travels in one of them. The console writes it, the API client carries
// it and the backend reads it, and all three import it from here so there is
// one definition of what the header holds rather than a writer and a reader
// that agree until one of them is edited.
//
// What the identity is *allowed to do* is not decided here. That is the
// backend's alone — see internal/backend/staff.go.
package staffauth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

// Headers the console speaks to the backend with.
const (
	// TokenHeader carries the secret that makes the identity believable.
	TokenHeader = "X-Staff-Token"

	// IdentityHeader carries who is acting: an Identity as JSON, URL-safe
	// base64, unpadded.
	//
	// One encoded header rather than three plain ones: a group in somebody's
	// identity provider can be called anything, including things an HTTP
	// header cannot carry, and a name that broke the request would lock a
	// whole collective out of its own content.
	IdentityHeader = "X-Staff-Identity"
)

// Identity is who is acting through the console.
//
// It is what the identity provider said about them at sign-in and nothing
// else.
type Identity struct {
	// Subject is the provider's stable identifier for the person. It is what
	// the audit log is keyed on, because it survives a change of name.
	Subject string `json:"subject"`

	// Name is what to call them on a screen and in the log.
	Name string `json:"name,omitempty"`

	// Groups are the identity-provider groups they are in.
	Groups []string `json:"groups,omitempty"`
}

// ErrNoSubject means the identity names nobody. A change nobody can be named
// for is the one thing the audit log exists to make impossible.
var ErrNoSubject = errors.New("identity has no subject")

// Encode renders the identity for the header.
func (i Identity) Encode() (string, error) {
	raw, err := json.Marshal(i)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// Decode reads the header back, refusing an identity with no subject.
func Decode(value string) (Identity, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return Identity{}, err
	}
	var identity Identity
	if err := json.Unmarshal(raw, &identity); err != nil {
		return Identity{}, err
	}
	if strings.TrimSpace(identity.Subject) == "" {
		return Identity{}, ErrNoSubject
	}
	return identity, nil
}
