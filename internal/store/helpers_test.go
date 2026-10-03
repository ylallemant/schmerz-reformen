package store

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	"github.com/ylallemant/schmerz-reformen/internal/models"
)

// account makes somebody who has signed in, the way a passkey registration
// leaves them: a random handle, a chosen name, and nothing else at all.
func account(t *testing.T, s *Store, name string) models.Account {
	t.Helper()

	handle := make([]byte, 32)
	if _, err := rand.Read(handle); err != nil {
		t.Fatalf("read randomness: %v", err)
	}

	created := &models.Account{Handle: handle, Name: name}
	first := &models.Credential{
		CredentialID: append([]byte("cred-"), handle...),
		PublicKey:    []byte("not a real key"),
		Name:         "a device",
	}
	if err := s.CreateAccount(context.Background(), created, first); err != nil {
		t.Fatalf("CreateAccount(%q): %v", name, err)
	}
	return *created
}

// collective makes a published collective, the way an administrator leaves
// one: named, addressed, and managed by a group of its own.
func collective(t *testing.T, s *Store, name string) models.Collective {
	t.Helper()

	created := &models.Collective{
		Name:      name,
		Slug:      name,
		AuthGroup: "group-" + models.Slugify(name),
		Status:    models.StatusPublished,
	}
	if err := s.CreateCollective(context.Background(), created); err != nil {
		t.Fatalf("CreateCollective(%q): %v", name, err)
	}
	return *created
}

// topic makes a published topic pinned somewhere.
func topic(t *testing.T, s *Store, collectiveID, title string, lat, lng float64) models.Topic {
	t.Helper()

	created := &models.Topic{
		CollectiveID: collectiveID,
		Kind:         models.TopicCut,
		Level:        models.LevelMunicipal,
		Title:        title,
		Location:     models.Location{Latitude: lat, Longitude: lng},
		Status:       models.StatusPublished,
	}
	if _, err := s.SaveTopic(context.Background(), created); err != nil {
		t.Fatalf("SaveTopic(%q): %v", title, err)
	}
	return *created
}

// action makes a published action at a given time.
func action(t *testing.T, s *Store, collectiveID, title string, startsAt time.Time) models.Action {
	t.Helper()

	created := &models.Action{
		CollectiveID: collectiveID,
		Kind:         models.ActionDemonstration,
		Title:        title,
		StartsAt:     startsAt,
		Status:       models.StatusPublished,
	}
	if _, err := s.SaveAction(context.Background(), created); err != nil {
		t.Fatalf("SaveAction(%q): %v", title, err)
	}
	return *created
}

// organisation puts an organisation in directly, the way an approved creation
// leaves one. Tests about the curation itself go through ProposeChange.
func organisation(t *testing.T, s *Store, name, parentID string) models.Organisation {
	t.Helper()

	created := models.Organisation{OrganisationValues: models.OrganisationValues{
		Name: name, Kind: models.MemberUnion, ParentID: parentID,
	}}
	if err := s.db.Create(&created).Error; err != nil {
		t.Fatalf("create organisation %q: %v", name, err)
	}
	return created
}
