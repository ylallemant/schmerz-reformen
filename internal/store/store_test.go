package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ylallemant/schmerz-reformen/internal/models"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(Options{Driver: DriverSQLite, DSN: filepath.Join(t.TempDir(), "test.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close(context.Background()) }) //nolint:errcheck

	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

func TestOpenRejectsBadOptions(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		want string
	}{
		{"unknown driver", Options{Driver: "mysql", DSN: "x"}, "unknown database driver"},
		{"empty driver", Options{DSN: "x"}, "unknown database driver"},
		{"missing dsn", Options{Driver: DriverSQLite}, "dsn is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Open(tt.opts)
			if err == nil {
				t.Fatalf("expected an error mentioning %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	s := newStore(t)
	if err := s.Migrate(); err != nil {
		t.Errorf("second Migrate: %v — migrations must be repeatable", err)
	}
}

func TestReadyReportsConnectivity(t *testing.T) {
	s := newStore(t)
	if err := s.Ready(context.Background()); err != nil {
		t.Errorf("Ready: %v", err)
	}

	s.Close(context.Background()) //nolint:errcheck
	if err := s.Ready(context.Background()); err == nil {
		t.Error("Ready succeeded on a closed database")
	}
}

// TestAccountHandleIsUnique: the handle is what an authenticator hands back at
// sign-in, and it is the only thing that names the account. Two accounts
// sharing one would make a sign-in ambiguous, which is not a state any code
// downstream is written to cope with.
func TestAccountHandleIsUnique(t *testing.T) {
	s := newStore(t)
	db := s.DB()

	handle := []byte("a-random-handle")
	if err := db.Create(&models.Account{Handle: handle, Name: "Someone"}).Error; err != nil {
		t.Fatalf("create account: %v", err)
	}
	if err := db.Create(&models.Account{Handle: handle}).Error; err == nil {
		t.Error("two accounts were allowed the same user handle")
	}
}

// TestAccountHoldsNoAddress is the property everything a reader does here
// rests on. A follow pairs an account with a political alliance; that is only
// defensible while the account is nobody, and it stays true as a fact about
// the schema rather than as a rule somebody has to remember.
func TestAccountHoldsNoAddress(t *testing.T) {
	s := newStore(t)

	columns, err := s.DB().Migrator().ColumnTypes(&models.Account{})
	if err != nil {
		t.Fatalf("read the account columns: %v", err)
	}
	for _, column := range columns {
		name := strings.ToLower(column.Name())
		if strings.Contains(name, "mail") || strings.Contains(name, "phone") {
			t.Errorf("an account carries a column called %q", column.Name())
		}
	}
}

// TestTheAuditLogNamesWhoDidIt: content is published without review, so the
// log is the other half of that bargain — every change traces back to a named
// person, and stays readable after the thing it was about is gone.
func TestTheAuditLogNamesWhoDidIt(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	mine := collective(t, s, "Bündnis Düsseldorf")
	other := collective(t, s, "Bündnis Köln")

	for _, entry := range []models.AuditEntry{
		{Actor: "sub-1", ActorName: "Dominique", Action: models.AuditPublish,
			SubjectType: "topic", SubjectID: models.NewID(), CollectiveID: mine.ID,
			Summary: "100 Millionen weniger"},
		{Actor: "sub-2", ActorName: "Camille", Action: models.AuditDelete,
			SubjectType: "action", SubjectID: models.NewID(), CollectiveID: other.ID,
			Summary: "Demo"},
	} {
		if err := s.Audit(ctx, entry); err != nil {
			t.Fatalf("Audit: %v", err)
		}
	}

	all, total, err := s.ListAudit(ctx, AuditQuery{})
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	if total != 2 || len(all) != 2 {
		t.Fatalf("%d entries, want both", total)
	}

	// An editor reads their own collective's history and nobody else's.
	scoped, total, err := s.ListAudit(ctx, AuditQuery{CollectiveIDs: []string{mine.ID}})
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	if total != 1 || scoped[0].ActorName != "Dominique" || scoped[0].Summary != "100 Millionen weniger" {
		t.Errorf("scoped log = %+v, want the one entry about this collective", scoped)
	}

	// No collectives is not "every collective".
	if none, total, _ := s.ListAudit(ctx, AuditQuery{CollectiveIDs: []string{}}); total != 0 || len(none) != 0 {
		t.Error("an editor of nothing was shown the whole log")
	}
}

func TestEnumValidation(t *testing.T) {
	if !models.StatusPublished.Valid() || models.PublishStatus("maybe").Valid() {
		t.Error("PublishStatus.Valid does not match the defined states")
	}
	if models.StatusDraft.Public() || !models.StatusArchived.Public() {
		t.Error("a draft is private and an archived page still answers")
	}
	if !models.TopicCut.Valid() || models.TopicKind("rumour").Valid() {
		t.Error("TopicKind.Valid does not match the defined kinds")
	}
	if !models.LevelMunicipal.Valid() || models.Level("galactic").Valid() {
		t.Error("Level.Valid does not match the defined levels")
	}
	if !models.ActionStrike.Valid() || models.ActionKind("riot").Valid() {
		t.Error("ActionKind.Valid does not match the defined kinds")
	}
	if !models.MemberUnion.Valid() || models.MemberKind("person").Valid() {
		t.Error("MemberKind.Valid does not match the defined kinds — and a member is never a person")
	}
	if !models.FollowTopic.Valid() || models.FollowTarget("account").Valid() {
		t.Error("FollowTarget.Valid does not match the defined targets")
	}
	if !DriverSQLite.Valid() || Driver("mysql").Valid() {
		t.Error("Driver.Valid does not match the supported engines")
	}
}
