package store

import (
	"context"
	"testing"

	"github.com/ylallemant/schmerz-reformen/internal/models"
)

// TestResubscribingIsOneDeviceNotTwo.
//
// A browser that re-subscribes is handed the same endpoint back. Two rows for
// it would send every notification twice to one screen, and the person would
// have no way to tell which of the two to remove.
func TestResubscribingIsOneDeviceNotTwo(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	camille := account(t, s, "Camille")

	for _, keys := range []struct{ p256dh, auth, label string }{
		{"first-key", "first-auth", "a phone"},
		{"fresh-key", "fresh-auth", "the same phone"},
	} {
		err := s.Subscribe(ctx, &models.PushSubscription{
			AccountID: camille.ID,
			Endpoint:  "https://push.example/abc",
			P256dh:    keys.p256dh,
			Auth:      keys.auth,
			Label:     keys.label,
		})
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
	}

	devices, err := s.ListSubscriptions(ctx, camille.ID)
	if err != nil {
		t.Fatalf("ListSubscriptions: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("%d subscriptions, want 1 for one browser", len(devices))
	}
	// The keys are overwritten rather than kept: a browser that lost its
	// permission and was granted it again generates a fresh pair, and the old
	// ones then encrypt to nothing.
	if devices[0].P256dh != "fresh-key" || devices[0].Auth != "fresh-auth" {
		t.Error("a re-subscription kept the old keys")
	}
	if devices[0].Label != "the same phone" {
		t.Errorf("label = %q, want the newer one", devices[0].Label)
	}
}

// TestADeadEndpointIsDeletedNotRetried.
//
// This is the whole of the delivery-failure story, and it is deliberately not
// a retry ladder. A mailbox can be temporarily full, so
// retrying it makes sense; a push endpoint answering 404 or 410 is not there
// any more and will not be.
func TestADeadEndpointIsDeletedNotRetried(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	camille := account(t, s, "Camille")

	err := s.Subscribe(ctx, &models.PushSubscription{
		AccountID: camille.ID, Endpoint: "https://push.example/gone",
		P256dh: "p", Auth: "a",
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := s.UnsubscribeEndpoint(ctx, "https://push.example/gone"); err != nil {
		t.Fatalf("UnsubscribeEndpoint: %v", err)
	}
	devices, _ := s.ListSubscriptions(ctx, camille.ID)
	if len(devices) != 0 {
		t.Error("a dead endpoint was kept")
	}
}

// TestANotificationExistsWhetherOrNotAPushDoes.
//
// The list is the channel and the push is a tap on the shoulder. An iPhone
// that has not installed the site receives no push at all, a permission can be
// refused, and an endpoint can die between one week and the next — so nothing
// may exist only as a push.
func TestANotificationExistsWhetherOrNotAPushDoes(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	camille := account(t, s, "Camille")

	// No subscription at all: the notification still has to land.
	err := s.Notify(ctx, &models.Notification{
		AccountID: camille.ID, Kind: models.NotifyTopicUpdate,
		Title: "Bündnis gegen Kürzungen", Body: "Der Rat hat die Abstimmung vertagt",
	})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}

	items, total, err := s.ListNotifications(ctx, camille.ID, 20, 0)
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("%d notifications, want 1", total)
	}
	unread, err := s.CountUnread(ctx, camille.ID)
	if err != nil {
		t.Fatalf("CountUnread: %v", err)
	}
	if unread != 1 {
		t.Errorf("unread = %d, want 1", unread)
	}

	if err := s.MarkNotificationRead(ctx, camille.ID, items[0].ID); err != nil {
		t.Fatalf("MarkNotificationRead: %v", err)
	}
	if unread, _ := s.CountUnread(ctx, camille.ID); unread != 0 {
		t.Errorf("unread = %d after reading, want 0", unread)
	}

	// Somebody else's notification is not theirs to read or remove.
	dominique := account(t, s, "Dominique")
	if err := s.DeleteNotification(ctx, dominique.ID, items[0].ID); err != nil {
		t.Fatalf("DeleteNotification: %v", err)
	}
	if _, total, _ := s.ListNotifications(ctx, camille.ID, 20, 0); total != 1 {
		t.Error("another account deleted somebody's notification")
	}
}
