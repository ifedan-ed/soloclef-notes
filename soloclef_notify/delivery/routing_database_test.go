package delivery_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"

	"git.danvics.com/danvics/soloclef/soloclef_notify/apns/apnstest"
	"git.danvics.com/danvics/soloclef/soloclef_notify/internal/notifytest"
	"git.danvics.com/danvics/soloclef/soloclef_notify/notify"
)

// appleToken is an APNs device token of Apple's shape, unique to the test.
func appleToken(user string, n int) string {
	return fmt.Sprintf("%s%056x", user[:8], n)
}

// received is a queued message as the worker reads it back: through JSON,
// where every number is a float64 and every map's keys are strings.
func received(t *testing.T, message produce.Message) *celery.Message {
	t.Helper()
	var args []any
	var kwargs map[string]any
	raw, _ := json.Marshal(message.Args)
	if err := json.Unmarshal(raw, &args); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(message.Kwargs)
	if err := json.Unmarshal(raw, &kwargs); err != nil {
		t.Fatal(err)
	}
	return &celery.Message{Task: message.Task, Args: args, Kwargs: kwargs, Retries: message.Retries}
}

// Each phone is pushed by its token's sender: an Expo token through Expo,
// an APNs token of the worker's environment through Apple (with no ticket
// kept: Apple has no receipts), and an APNs token of the other environment
// not at all, and kept.
func TestEachPhoneIsPushedByItsTokensSender(t *testing.T) {
	h := newHarness(t)
	user := notifytest.Account(t, h.pool)
	h.phone(t, user, "ExponentPushToken[android-"+user[:8]+"]", true)
	production := appleToken(user, 1)
	sandbox := appleToken(user, 2)
	h.iphone(t, user, production, "production")
	h.iphone(t, user, sandbox, "sandbox")
	id := h.studioRow(t, user)

	if err := h.jobs.Deliver(context.Background(), deliverMessage([]int64{id}, 0)); err != nil {
		t.Fatal(err)
	}
	if sent := h.expo.Messages(); len(sent) != 1 || sent[0].To != "ExponentPushToken[android-"+user[:8]+"]" {
		t.Errorf("Expo was sent %+v", sent)
	}
	pushes := h.apple.Pushes()
	if len(pushes) != 1 || pushes[0].Token != production {
		t.Fatalf("Apple was sent %+v", pushes)
	}
	push := pushes[0]
	aps := push.Payload.APS
	if aps.Alert.Title != "Video ready" || aps.Badge == nil || *aps.Badge != 1 || aps.Sound != "default" || aps.ThreadID != "output" {
		t.Errorf("aps = %+v", aps)
	}
	// What the app reads on a tap, as the Expo push carries it.
	if data := push.Payload.Body; data["path"] != "/inbox" || data["kind"] != "output" || data["v"] != float64(1) || data["server"] != "https://notes.example.com" {
		t.Errorf("body = %v", data)
	}
	if expires := push.Header.Get("apns-expiration"); expires == "" {
		t.Error("the push has no expiration")
	}
	if push.Header.Get("apns-collapse-id") != "" {
		t.Error("a Studio output, which never collapses, carried a collapse id")
	}
	if n := notifytest.Int64(t, h.pool, `SELECT count(*) FROM push_tickets WHERE token IN ($1, $2)`, production, sandbox); n != 0 {
		t.Errorf("%d tickets kept for Apple's pushes", n)
	}
	if n := notifytest.Int64(t, h.pool, `SELECT count(*) FROM push_devices WHERE token IN ($1, $2)`, production, sandbox); n != 2 {
		t.Error("an iPhone was forgotten")
	}
	notifytest.Exec(t, h.pool, `DELETE FROM push_tickets WHERE token = $1`, "ExponentPushToken[android-"+user[:8]+"]")
}

// A row that stands for several events (its collapse key) collapses on the
// iPhone: a newer push of the group replaces the older one.
func TestARowOfAGroupCollapsesOnTheIPhone(t *testing.T) {
	h := newHarness(t)
	user := notifytest.Account(t, h.pool)
	phone := appleToken(user, 1)
	h.iphone(t, user, phone, "production")
	id := notifytest.Int64(t, h.pool, `
		INSERT INTO notifications (user_id, type, title, message, metadata)
		VALUES ($1::uuid, 'document_processing', '3 imports failed', 'x', jsonb_build_object(
			'collapse_key', 'source:failed',
			'push', jsonb_build_object('kind', 'source', 'title', 'Imports failed', 'body', '', 'path', '/inbox')))
		RETURNING id`, user)
	if err := h.jobs.Deliver(context.Background(), deliverMessage([]int64{id}, 0)); err != nil {
		t.Fatal(err)
	}
	pushes := h.apple.Pushes()
	if len(pushes) != 1 || pushes[0].Header.Get("apns-collapse-id") != "source:failed" || pushes[0].Payload.APS.ThreadID != "source" {
		t.Errorf("pushed %+v", pushes)
	}
}

// Apple's word that a token is gone (410, or BadDeviceToken in the token's
// own environment) deletes it at once.
func TestAPhoneAppleSaysIsGoneIsForgotten(t *testing.T) {
	h := newHarness(t)
	user := notifytest.Account(t, h.pool)
	removed, bad, kept := appleToken(user, 1), appleToken(user, 2), appleToken(user, 3)
	for _, token := range []string{removed, bad, kept} {
		h.iphone(t, user, token, "production")
	}
	h.apple.Answer(removed, apnstest.Answer{Status: http.StatusGone, Reason: "Unregistered"})
	h.apple.Answer(bad, apnstest.Answer{Status: http.StatusBadRequest, Reason: "BadDeviceToken"})
	// A refusal of this server's own topic says nothing about the phone.
	h.apple.Answer(kept, apnstest.Answer{Status: http.StatusBadRequest, Reason: "DeviceTokenNotForTopic"})
	if err := h.jobs.Deliver(context.Background(), deliverMessage([]int64{h.studioRow(t, user)}, 0)); err != nil {
		t.Fatal(err)
	}
	if n := notifytest.Int64(t, h.pool, `SELECT count(*) FROM push_devices WHERE token IN ($1, $2)`, removed, bad); n != 0 {
		t.Errorf("%d gone iPhones are still registered", n)
	}
	if n := notifytest.Int64(t, h.pool, `SELECT count(*) FROM push_devices WHERE token = $1`, kept); n != 1 {
		t.Error("a phone was forgotten for this server's own mistake")
	}
	for _, m := range h.published.Messages() {
		if m.Task == notify.DeliverTask && m.Retries > 0 {
			t.Errorf("an answer that will not change was retried: %+v", m)
		}
	}
}

// A push Apple put off (a 429 or a 5xx) for one phone is tried again from
// the queue for that phone alone: the phones that took it are not pushed it
// twice.
func TestAPushApplePutOffIsTriedAgainForThatPhoneAlone(t *testing.T) {
	h := newHarness(t)
	user := notifytest.Account(t, h.pool)
	android := "ExponentPushToken[late-" + user[:8] + "]"
	h.phone(t, user, android, true)
	took, later := appleToken(user, 1), appleToken(user, 2)
	h.iphone(t, user, took, "production")
	laterID := h.iphone(t, user, later, "production")
	h.apple.Answer(later, apnstest.Answer{Status: http.StatusServiceUnavailable, Reason: "ServiceUnavailable"})
	id := h.studioRow(t, user)
	before := len(h.published.Messages())

	if err := h.jobs.Deliver(context.Background(), deliverMessage([]int64{id}, 0)); err != nil {
		t.Fatal(err)
	}
	retries := h.published.Messages()[before:]
	var retry *produce.Message
	for i := range retries {
		if retries[i].Task == notify.DeliverTask {
			retry = &retries[i]
		}
	}
	if retry == nil || retry.Retries != 1 || retry.Countdown != 30*time.Second {
		t.Fatalf("published %+v", retries)
	}
	if phones := fmt.Sprint(retry.Kwargs[notify.DevicesKwarg]); phones != fmt.Sprintf("map[%d:[%d]]", id, laterID) {
		t.Errorf("the retry names %s", phones)
	}
	// The retry, read back as the worker reads it, pushes that phone and no
	// other.
	if err := h.jobs.Deliver(context.Background(), received(t, *retry)); err != nil {
		t.Fatal(err)
	}
	if accepted := h.apple.Accepted(); fmt.Sprint(accepted) != fmt.Sprint([]string{took, later}) {
		t.Errorf("Apple accepted %v", accepted)
	}
	if sent := h.expo.Messages(); len(sent) != 1 {
		t.Errorf("the Android phone was pushed %d times", len(sent))
	}
	notifytest.Exec(t, h.pool, `DELETE FROM push_tickets WHERE token = $1`, android)
}

// With no Apple key, iPhones on APNs tokens are not pushed (and not
// forgotten: they are pushed again once a key is set); Android phones are.
func TestWithoutAnAppleKeyIPhonesAreLeftAlone(t *testing.T) {
	h := newHarness(t)
	h.jobs.Sending.APNs, h.jobs.Sending.APNsEnvironment = nil, ""
	if err := h.jobs.Check(); err != nil {
		t.Fatal(err)
	}
	user := notifytest.Account(t, h.pool)
	android := "ExponentPushToken[nokey-" + user[:8] + "]"
	h.phone(t, user, android, true)
	iphone := appleToken(user, 1)
	h.iphone(t, user, iphone, "production")
	if err := h.jobs.Deliver(context.Background(), deliverMessage([]int64{h.studioRow(t, user)}, 0)); err != nil {
		t.Fatal(err)
	}
	if len(h.apple.Pushes()) != 0 {
		t.Error("Apple was pushed with no key")
	}
	if sent := h.expo.Messages(); len(sent) != 1 || sent[0].To != android {
		t.Errorf("Expo was sent %+v", sent)
	}
	if n := notifytest.Int64(t, h.pool, `SELECT count(*) FROM push_devices WHERE token = $1`, iphone); n != 1 {
		t.Error("the iPhone was forgotten")
	}
	notifytest.Exec(t, h.pool, `DELETE FROM push_tickets WHERE token = $1`, android)

	// APNs and its environment come together or not at all.
	h.jobs.Sending.APNsEnvironment = "production"
	if h.jobs.Check() == nil {
		t.Error("an environment with no APNs sender was accepted")
	}
}

// A retry names its rows' phones; one that names another row, or names
// none, is not the job's own and is refused.
func TestARetryThatNamesOtherRowsIsRefused(t *testing.T) {
	h := newHarness(t)
	for name, kwargs := range map[string]map[string]any{
		"another row": {notify.DevicesKwarg: map[string]any{"99999": []any{float64(1)}}},
		"no rows":     {notify.DevicesKwarg: map[string]any{}},
		"not phones":  {notify.DevicesKwarg: map[string]any{"5": "x"}},
		"not a map":   {notify.DevicesKwarg: []any{float64(1)}},
	} {
		msg := &celery.Message{Task: notify.DeliverTask, Args: []any{[]any{float64(5)}}, Kwargs: kwargs}
		if err := h.jobs.Deliver(context.Background(), msg); err == nil {
			t.Errorf("%s was taken", name)
		}
	}
}
