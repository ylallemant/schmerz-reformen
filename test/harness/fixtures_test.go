package harness

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestTheFixturesAreUsable is the cheap guard that stops --seeding failing at
// the one moment somebody wanted to see the thing working.
func TestTheFixturesAreUsable(t *testing.T) {
	var file seedFile
	if err := json.Unmarshal(fixtures, &file); err != nil {
		t.Fatalf("the fixtures do not parse: %v", err)
	}
	if len(file.Collectives) == 0 {
		t.Fatal("the fixtures are empty")
	}

	slugs, groups := map[string]bool{}, map[string]bool{}
	for _, collective := range file.Collectives {
		if collective.Name == "" || collective.Slug == "" || collective.AuthGroup == "" {
			t.Errorf("collective %q needs a name, an address and a group", collective.Name)
		}
		if slugs[collective.Slug] {
			t.Errorf("two collectives answer at %q; the second would be refused", collective.Slug)
		}
		slugs[collective.Slug] = true
		// One group per collective, so a local run with
		// --development-groups shows an editor exactly one of them.
		if groups[collective.AuthGroup] {
			t.Errorf("two collectives are managed by %q", collective.AuthGroup)
		}
		groups[collective.AuthGroup] = true

		// Fixtures must never put words in the mouth of a real organisation.
		// The marker in the name is what a reader of a local run sees.
		if !strings.Contains(collective.Name, "(Beispiel)") {
			t.Errorf("collective %q does not say it is an example", collective.Name)
		}

		titles := map[string]bool{}
		for _, topic := range collective.Topics {
			if topic.Title == "" || topic.Kind == "" || topic.Level == "" {
				t.Errorf("a topic of %q needs a title, a kind and a level", collective.Name)
			}
			if topic.Amount < 0 {
				t.Errorf("%q has a negative amount; the sign is in the kind", topic.Title)
			}
			titles[topic.Title] = true
		}

		for _, action := range collective.Actions {
			if action.Title == "" || action.Kind == "" {
				t.Errorf("an action of %q needs a title and a kind", collective.Name)
			}
			if action.InDays <= 0 {
				t.Errorf("%q would be seeded in the past", action.Title)
			}
			if _, err := time.Parse("15:04", action.At); err != nil {
				t.Errorf("%q is at %q, which is not HH:MM", action.Title, action.At)
			}
			// An action names its topic by title; a typo here would seed it
			// with no topic and nothing would say so.
			if action.Topic != "" && !titles[action.Topic] {
				t.Errorf("%q is about %q, which is not one of its collective's topics",
					action.Title, action.Topic)
			}
		}
	}
}

// TestASeededActionIsAtTheTimeItSaysWhereItHappens: the fixture's "17:00" is
// 17:00 in the installation's zone, whatever zone the machine running the
// seeder is in.
func TestASeededActionIsAtTheTimeItSaysWhereItHappens(t *testing.T) {
	zone, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}
	s := &seeder{zone: zone}

	starts, err := s.startOf(SeedAction{InDays: 5, At: "17:00"})
	if err != nil {
		t.Fatalf("startOf: %v", err)
	}
	local := starts.In(zone)
	if local.Hour() != 17 || local.Minute() != 0 {
		t.Errorf("starts at %s, want 17:00 in Berlin", local.Format("15:04"))
	}
	if !starts.After(time.Now()) {
		t.Error("a seeded action is in the past")
	}

	if _, err := s.startOf(SeedAction{InDays: 1, At: "five o'clock"}); err == nil {
		t.Error("a time that is not HH:MM was accepted")
	}
}
