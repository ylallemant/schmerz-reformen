package store

import (
	"context"
	"testing"
	"time"

	"github.com/ylallemant/schmerz-reformen/internal/models"
	"github.com/ylallemant/schmerz-reformen/internal/store/storetest"
)

// TestGroupsAreNamedOnceAndKept: from the slug when the entry is created, and
// never renamed with it — the people in a group would lose their roles.
func TestGroupsAreNamedOnceAndKept(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	c := collective(t, s, "Bündnis Düsseldorf")
	if c.AdminGroup != "schmerz-collective-buendnis-duesseldorf-admins" ||
		c.AuthorGroup != "schmerz-collective-buendnis-duesseldorf-authors" {
		t.Errorf("collective groups = %q / %q", c.AdminGroup, c.AuthorGroup)
	}
	c.Slug = "duesseldorf"
	if err := s.SaveCollective(ctx, &c); err != nil {
		t.Fatal(err)
	}
	again, _ := s.Collective(ctx, c.ID)
	if again.Slug != "duesseldorf" || again.AdminGroup != "schmerz-collective-buendnis-duesseldorf-admins" {
		t.Errorf("after a new slug: slug %q, group %q — the group must not follow", again.Slug, again.AdminGroup)
	}

	first := organisation(t, s, "ver.di Düsseldorf", "")
	second := organisation(t, s, "ver.di Düsseldorf", "")
	if first.Slug != "ver-di-duesseldorf" || second.Slug != "ver-di-duesseldorf-2" {
		t.Errorf("slugs = %q, %q: two of the same name need groups of their own", first.Slug, second.Slug)
	}
	if first.AdminGroup != "schmerz-organisation-ver-di-duesseldorf-admins" ||
		first.MemberGroup != "schmerz-organisation-ver-di-duesseldorf-members" {
		t.Errorf("organisation groups = %q / %q", first.AdminGroup, first.MemberGroup)
	}
	first.Name = "ver.di Bezirk Düsseldorf"
	first.Slug, first.AdminGroup = "something-else", "something-else"
	if err := s.SaveOrganisation(ctx, &first); err != nil {
		t.Fatal(err)
	}
	read, _ := s.Organisation(ctx, first.ID)
	if read.Name != "ver.di Bezirk Düsseldorf" || read.Slug != "ver-di-duesseldorf" ||
		read.AdminGroup != "schmerz-organisation-ver-di-duesseldorf-admins" {
		t.Errorf("after a save: %q / %q / %q — the slug and groups are not the form's", read.Name, read.Slug, read.AdminGroup)
	}
}

// legacyCollectiveRow is a collective as it was, with one free-typed group.
type legacyCollectiveRow struct {
	ID        string `gorm:"primaryKey;size:36"`
	CreatedAt time.Time
	UpdatedAt time.Time
	Name      string `gorm:"size:160"`
	Slug      string `gorm:"uniqueIndex;size:96"`
	AuthGroup string `gorm:"index;size:128"`
	Status    string `gorm:"size:16"`
}

func (legacyCollectiveRow) TableName() string { return "collectives" }

// TestCollectivesAndOrganisationsAreGivenTheirGroups: a database from before
// the roles comes out with every collective and organisation named, the old
// group column gone, and the curation tables with it.
func TestCollectivesAndOrganisationsAreGivenTheirGroups(t *testing.T) {
	driver, dsn := storetest.Database(t)
	s, err := Open(Options{Driver: Driver(driver), DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(context.Background()) }) //nolint:errcheck

	if err := s.db.AutoMigrate(&legacyCollectiveRow{}); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Create(&legacyCollectiveRow{ID: "c1", Name: "Köln", Slug: "koeln", AuthGroup: "buendnis-koeln", Status: "published"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.db.Exec("CREATE TABLE organisation_changes (id varchar(36))").Error; err != nil {
		t.Fatal(err)
	}

	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	// An organisation that predates slugs.
	if err := s.db.Exec("INSERT INTO organisations (id, name, kind, slug) VALUES ('o1', 'Mieterverein', 'association', '')").Error; err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	got, err := s.Collective(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if got.AdminGroup != "schmerz-collective-koeln-admins" || got.AuthorGroup != "schmerz-collective-koeln-authors" {
		t.Errorf("collective groups = %q / %q", got.AdminGroup, got.AuthorGroup)
	}
	organisation, _ := s.Organisation(context.Background(), "o1")
	if organisation.Slug != "mieterverein" || organisation.MemberGroup != "schmerz-organisation-mieterverein-members" {
		t.Errorf("organisation = %q / %q", organisation.Slug, organisation.MemberGroup)
	}
	if s.db.Migrator().HasColumn(&models.Collective{}, "auth_group") {
		t.Error("the old group column survived")
	}
	if s.db.Migrator().HasTable("organisation_changes") {
		t.Error("the curation table survived")
	}
}
