package delivery_test

import (
	"context"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_notify/internal/notifytest"
	"git.danvics.com/danvics/soloclef/soloclef_notify/notify"
)

const storeListing = "https://apps.apple.com/app/id6821393434"

// tellUpdate runs notify.AppUpdated for the phones and answers the phones it
// told, failing the test on an error.
func (h *harness) tellUpdate(t *testing.T, user, version string, devices ...int64) []int64 {
	t.Helper()
	told, err := h.notifier.AppUpdated(context.Background(), notify.AppUpdate{
		UserID: user, Version: version, StoreURL: storeListing, Devices: devices,
	})
	if err != nil {
		t.Fatal(err)
	}
	return told
}

// deliverQueued runs every delivery the notifier queued since the last call,
// as the worker would take them off the queue.
func (h *harness) deliverQueued(t *testing.T, from int) int {
	t.Helper()
	messages := h.published.Messages()
	for _, message := range messages[from:] {
		if message.Task != notify.DeliverTask {
			continue
		}
		if err := h.jobs.Deliver(context.Background(), received(t, message)); err != nil {
			t.Fatal(err)
		}
	}
	return len(messages)
}

// An update is pushed to the iPhones the check named and to no other phone
// of the account: not an iPhone already on the new version, not an Android
// phone. The push says the version, opens no route (the phone opens the App
// Store for its kind) and collapses with an older update's push.
func TestAnUpdateIsPushedOnlyToThePhonesNamed(t *testing.T) {
	h := newHarness(t)
	user := notifytest.Account(t, h.pool)
	outdated, current := appleToken(user, 1), appleToken(user, 2)
	outdatedID := h.iphone(t, user, outdated, "production")
	h.iphone(t, user, current, "production")
	h.phone(t, user, "ExponentPushToken[android-"+user[:8]+"]", true)

	if told := h.tellUpdate(t, user, "1.2.0", outdatedID); len(told) != 1 || told[0] != outdatedID {
		t.Fatalf("told %v", told)
	}
	h.deliverQueued(t, 0)

	pushes := h.apple.Pushes()
	if len(pushes) != 1 || pushes[0].Token != outdated {
		t.Fatalf("Apple was sent %+v", pushes)
	}
	push := pushes[0]
	if aps := push.Payload.APS; aps.Alert.Title != "Soloclef 1.2.0 is out" || aps.ThreadID != "update" {
		t.Errorf("aps = %+v", aps)
	}
	if push.Header.Get("apns-collapse-id") != "app:update" {
		t.Errorf("collapse id %q", push.Header.Get("apns-collapse-id"))
	}
	if body := push.Payload.Body; body["kind"] != "update" || body["path"] != "" {
		t.Errorf("body = %v", body)
	}
	if sent := h.expo.Messages(); len(sent) != 0 {
		t.Errorf("an Android phone was pushed an iPhone update: %+v", sent)
	}

	var title, version, link string
	if err := h.pool.QueryRow(context.Background(), `
		SELECT title, metadata->>'version', metadata->>'store_url' FROM notifications
		WHERE user_id = $1::uuid AND type = 'app_update'`, user).Scan(&title, &version, &link); err != nil {
		t.Fatal(err)
	}
	if title != "Soloclef 1.2.0 is out" || version != "1.2.0" || link != storeListing {
		t.Errorf("row = %q, %q, %q", title, version, link)
	}
}

// A phone is told once per version: a second pass for the same version
// claims nothing and queues nothing. A newer version tells it again and
// rewrites the account's one update row (unread again, the new version)
// rather than adding a second.
func TestAPhoneIsToldOncePerVersion(t *testing.T) {
	h := newHarness(t)
	user := notifytest.Account(t, h.pool)
	phone := h.iphone(t, user, appleToken(user, 1), "production")

	h.tellUpdate(t, user, "1.2.0", phone)
	queued := len(h.published.Messages())
	if told := h.tellUpdate(t, user, "1.2.0", phone); len(told) != 0 {
		t.Errorf("told again for the same version: %v", told)
	}
	if again := len(h.published.Messages()); again != queued {
		t.Errorf("a second pass queued %d more pushes", again-queued)
	}

	notifytest.Exec(t, h.pool, `UPDATE notifications SET read = true WHERE user_id = $1::uuid`, user)
	if told := h.tellUpdate(t, user, "1.3.0", phone); len(told) != 1 {
		t.Fatalf("a newer version told %v", told)
	}
	if n := notifytest.Int64(t, h.pool, `SELECT count(*) FROM notifications WHERE user_id = $1::uuid`, user); n != 1 {
		t.Errorf("%d update rows; the newer version should rewrite the one", n)
	}
	var title string
	var read bool
	if err := h.pool.QueryRow(context.Background(), `SELECT title, read FROM notifications WHERE user_id = $1::uuid`, user).Scan(&title, &read); err != nil {
		t.Fatal(err)
	}
	if title != "Soloclef 1.3.0 is out" || read {
		t.Errorf("row = %q, read %v", title, read)
	}
}

// Only the owner's phones are claimed: an id of another account's phone in
// the list is not marked told and not pushed.
func TestAnUpdateClaimsOnlyTheOwnersPhones(t *testing.T) {
	h := newHarness(t)
	ada, bob := notifytest.Account(t, h.pool), notifytest.Account(t, h.pool)
	hers := h.iphone(t, ada, appleToken(ada, 1), "production")
	his := h.iphone(t, bob, appleToken(bob, 1), "production")

	if told := h.tellUpdate(t, ada, "1.2.0", hers, his); len(told) != 1 || told[0] != hers {
		t.Fatalf("told %v", told)
	}
	if n := notifytest.Int64(t, h.pool, `SELECT count(*) FROM push_devices WHERE id = $1 AND update_notified_version IS NULL`, his); n != 1 {
		t.Error("another account's phone was marked told")
	}
	h.deliverQueued(t, 0)
	if pushes := h.apple.Pushes(); len(pushes) != 1 || pushes[0].Token != appleToken(ada, 1) {
		t.Errorf("Apple was sent %+v", pushes)
	}
}

// The update kind has its own switch: off, the row is written and nothing
// is pushed; another kind off leaves it on.
func TestAnUpdateObeysItsOwnSwitch(t *testing.T) {
	for name, tc := range map[string]struct {
		choices string
		pushed  int
	}{
		"update off":       {`{"update": false}`, 0},
		"another kind off": {`{"output": false}`, 1},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			user := notifytest.Account(t, h.pool)
			phone := h.iphone(t, user, appleToken(user, 1), "production")
			notifytest.Exec(t, h.pool, `INSERT INTO push_preferences (user_id, choices) VALUES ($1::uuid, $2::jsonb)`, user, tc.choices)

			h.tellUpdate(t, user, "1.2.0", phone)
			h.deliverQueued(t, 0)
			if pushes := h.apple.Pushes(); len(pushes) != tc.pushed {
				t.Errorf("%d pushes, want %d", len(pushes), tc.pushed)
			}
			if n := notifytest.Int64(t, h.pool, `SELECT count(*) FROM notifications WHERE user_id = $1::uuid AND type = 'app_update'`, user); n != 1 {
				t.Errorf("%d update rows; the inbox row is written whatever the switch", n)
			}
		})
	}
}
