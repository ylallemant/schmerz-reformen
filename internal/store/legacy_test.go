package store

import (
	"context"
	"testing"
	"time"

	"github.com/ylallemant/schmerz-reformen/internal/models"
	"github.com/ylallemant/schmerz-reformen/internal/store/storetest"
)

// legacyMemberRow is a member row as it was while each collective kept its own
// copy of every organisation.
type legacyMemberRow struct {
	ID           string `gorm:"primaryKey;size:36"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
	CollectiveID string `gorm:"index;size:36"`
	Name         string `gorm:"size:160"`
	Kind         string `gorm:"size:16"`
	Website      string `gorm:"size:512"`
	LogoID       string `gorm:"size:36"`
	Position     int
}

func (legacyMemberRow) TableName() string { return "collective_members" }

// TestMembersMoveIntoOrganisations: a database from before organisations were
// shared comes out with one organisation per distinct member, the member rows
// linked to them, the logos handed over, and the old columns gone.
func TestMembersMoveIntoOrganisations(t *testing.T) {
	driver, dsn := storetest.Database(t)
	s, err := Open(Options{Driver: Driver(driver), DSN: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close(context.Background()) }) //nolint:errcheck

	// The old shape, written before anything has migrated.
	if err := s.db.AutoMigrate(&models.Collective{}, &legacyMemberRow{}, &models.Media{}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"duesseldorf", "koeln"} {
		if err := s.db.Omit("Members").Create(&models.Collective{Model: models.Model{ID: id}, Name: id, Slug: id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	created := time.Now().Add(-time.Hour)
	rows := []legacyMemberRow{
		{ID: "m1", CollectiveID: "duesseldorf", Name: "ver.di", Kind: "union", Website: "https://verdi.example", LogoID: "logo-1", Position: 1},
		{ID: "m2", CollectiveID: "koeln", Name: "ver.di ", Kind: "union", Website: "https://verdi.example", Position: 1},
		{ID: "m3", CollectiveID: "koeln", Name: "Mieterverein", Kind: "nonsense", Position: 2},
		{ID: "m4", CollectiveID: "koeln", Name: "VER.DI", Website: "https://verdi.example", Position: 3},
	}
	for i := range rows {
		rows[i].CreatedAt = created.Add(time.Duration(i) * time.Minute)
		if err := s.db.Create(&rows[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := s.db.Create(&models.Media{Model: models.Model{ID: "logo-1"}, CollectiveID: "duesseldorf", Key: "media/logo-1.png"}).Error; err != nil {
		t.Fatal(err)
	}

	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var organisations []models.Organisation
	if err := s.db.Order("name asc").Find(&organisations).Error; err != nil {
		t.Fatal(err)
	}
	if len(organisations) != 2 {
		t.Fatalf("%d organisations, want 2: the same union entered twice is one", len(organisations))
	}
	union := organisations[1]
	if union.Name != "ver.di" || union.LogoID != "logo-1" {
		t.Errorf("union = %q with logo %q", union.Name, union.LogoID)
	}
	if organisations[0].Kind != models.MemberOther {
		t.Errorf("an unknown kind became %q, want other", organisations[0].Kind)
	}

	var members []models.CollectiveMember
	if err := s.db.Order("id asc").Find(&members).Error; err != nil {
		t.Fatal(err)
	}
	if len(members) != 3 {
		t.Fatalf("%d member rows, want 3: Köln listed ver.di twice", len(members))
	}
	for _, member := range members {
		if member.OrganisationID == "" {
			t.Errorf("member %s links to nothing", member.ID)
		}
	}

	var logo models.Media
	if err := s.db.First(&logo, "id = ?", "logo-1").Error; err != nil {
		t.Fatal(err)
	}
	if logo.OrganisationID != union.ID || logo.CollectiveID != "" {
		t.Errorf("logo belongs to collective %q / organisation %q, want the organisation",
			logo.CollectiveID, logo.OrganisationID)
	}

	for _, column := range legacyMemberColumns {
		if s.db.Migrator().HasColumn(&models.CollectiveMember{}, column) {
			t.Errorf("column %q survived the move", column)
		}
	}

	// And a second start finds nothing to do.
	if err := s.Migrate(); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	var count int64
	s.db.Model(&models.Organisation{}).Count(&count) //nolint:errcheck
	if count != 2 {
		t.Errorf("%d organisations after a second start, want still 2", count)
	}
}
