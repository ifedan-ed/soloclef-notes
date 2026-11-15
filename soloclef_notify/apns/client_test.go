package apns_test

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_notify/apns"
	"git.danvics.com/danvics/soloclef/soloclef_notify/apns/apnstest"
	"git.danvics.com/danvics/soloclef/soloclef_notify/pushsend"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// token is a device token of Apple's shape: 32 bytes in hex.
func token(n int) string { return fmt.Sprintf("%064x", n) }

var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// A push goes over HTTP/2 to Apple's path for the token, with the headers
// Apple asks for, and carries the alert, badge, sound and thread in "aps"
// and the app's data under "body", where the app reads it on a tap.
func TestAPushIsSentAsAppleAsks(t *testing.T) {
	fake := apnstest.New(t)
	client := fake.Client(t, quiet())
	now := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	client.Now = func() time.Time { return now }
	badge := 3
	tickets, err := client.Send(context.Background(), []pushsend.Message{{
		To: token(1), Title: "Video ready", Body: "Cardiology", Sound: "default", Badge: &badge,
		Data:     map[string]any{"kind": "output", "path": "/notebook/7?pane=studio", "v": 1, "server": "https://notes.example.com"},
		ThreadID: "output", CollapseID: "source:failed", TTL: 24 * time.Hour, ChannelID: "soloclef",
	}})
	if err != nil {
		t.Fatal(err)
	}
	pushes := fake.Pushes()
	if len(tickets) != 1 || !tickets[0].Accepted || tickets[0].Gone || tickets[0].Retry || len(pushes) != 1 {
		t.Fatalf("tickets %+v, pushes %d", tickets, len(pushes))
	}
	push := pushes[0]
	if push.Proto != 2 {
		t.Errorf("sent over HTTP/%d; Apple takes HTTP/2 only", push.Proto)
	}
	if push.Token != token(1) {
		t.Errorf("pushed to %q", push.Token)
	}
	for header, want := range map[string]string{
		"apns-topic":       apnstest.Topic,
		"apns-push-type":   "alert",
		"apns-priority":    "10",
		"apns-collapse-id": "source:failed",
		"apns-expiration":  strconv.FormatInt(now.Add(24*time.Hour).Unix(), 10),
		"content-type":     "application/json",
	} {
		if got := push.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if id := push.Header.Get("apns-id"); !uuid.MatchString(id) || tickets[0].Reference != id {
		t.Errorf("apns-id %q, ticket reference %q", id, tickets[0].Reference)
	}
	if !strings.HasPrefix(push.Header.Get("authorization"), "bearer ey") {
		t.Errorf("authorization = %q", push.Header.Get("authorization"))
	}
	aps := push.Payload.APS
	if aps.Alert.Title != "Video ready" || aps.Alert.Body != "Cardiology" || aps.Badge == nil || *aps.Badge != 3 || aps.Sound != "default" || aps.ThreadID != "output" {
		t.Errorf("aps = %+v", aps)
	}
	data := push.Payload.Body
	if data["kind"] != "output" || data["path"] != "/notebook/7?pane=studio" || data["v"] != float64(1) || data["server"] != "https://notes.example.com" || len(data) != 4 {
		t.Errorf("body = %v", data)
	}

	// A push that never collapses and has no TTL sends neither header, and
	// a collapse key over Apple's 64 bytes goes as its SHA-256, so two long
	// keys stay apart.
	long := strings.Repeat("automation:", 8)
	if _, err := client.Send(context.Background(), []pushsend.Message{{To: token(2), Title: "x"}, {To: token(3), Title: "x", CollapseID: long}}); err != nil {
		t.Fatal(err)
	}
	for _, push := range fake.Pushes()[1:] {
		switch push.Token {
		case token(2):
			if _, has := push.Header["Apns-Collapse-Id"]; has {
				t.Error("a push that never collapses sent a collapse id")
			}
			if _, has := push.Header["Apns-Expiration"]; has {
				t.Error("a push with no TTL sent an expiration")
			}
		case token(3):
			sum := sha256.Sum256([]byte(long))
			if got := push.Header.Get("apns-collapse-id"); got != hex.EncodeToString(sum[:]) {
				t.Errorf("a long collapse key went as %q", got)
			}
		}
	}
}

// Each of Apple's answers is one decision: accepted; gone (deleted) for a
// 410 or BadDeviceToken; tried again later for a 429 or a 5xx; and logged,
// with the token kept, for a refusal of this server's own setup, which
// would read the same for every phone.
func TestApplesAnswersAreDecisions(t *testing.T) {
	fake := apnstest.New(t)
	answers := []apnstest.Answer{
		{Status: http.StatusOK},
		{Status: http.StatusGone, Reason: "Unregistered"},
		{Status: http.StatusGone, Reason: "ExpiredToken"},
		{Status: http.StatusBadRequest, Reason: "BadDeviceToken"},
		{Status: http.StatusTooManyRequests, Reason: "TooManyRequests"},
		{Status: http.StatusInternalServerError, Reason: "InternalServerError"},
		{Status: http.StatusServiceUnavailable, Reason: "ServiceUnavailable"},
		{Status: http.StatusBadRequest, Reason: "DeviceTokenNotForTopic"},
		{Status: http.StatusForbidden, Reason: "InvalidProviderToken"},
		{Status: http.StatusBadRequest, Reason: "PayloadEmpty"},
	}
	type want struct {
		accepted, gone, retry bool
		reason                string
	}
	wants := []want{
		{accepted: true}, {gone: true, reason: "Unregistered"}, {gone: true, reason: "ExpiredToken"}, {gone: true, reason: "BadDeviceToken"},
		{retry: true, reason: "TooManyRequests"}, {retry: true, reason: "InternalServerError"}, {retry: true, reason: "ServiceUnavailable"},
		{reason: "DeviceTokenNotForTopic"}, {reason: "InvalidProviderToken"}, {reason: "PayloadEmpty"},
	}
	messages := make([]pushsend.Message, len(answers))
	for i, answer := range answers {
		fake.Answer(token(i), answer)
		messages[i] = pushsend.Message{To: token(i), Title: "x"}
	}
	tickets, err := fake.Client(t, quiet()).Send(context.Background(), messages)
	if err != nil {
		t.Fatal(err)
	}
	for i, ticket := range tickets {
		got := want{accepted: ticket.Accepted, gone: ticket.Gone, retry: ticket.Retry, reason: ticket.Error}
		if got != wants[i] {
			t.Errorf("HTTP %d %s: %+v, want %+v", answers[i].Status, answers[i].Reason, got, wants[i])
		}
		if !uuid.MatchString(ticket.Reference) {
			t.Errorf("HTTP %d: no apns-id to log (%q)", answers[i].Status, ticket.Reference)
		}
	}
}

// When Apple says the provider token expired, a new one is signed and the
// push sent again at once, with the new token; the refusal was about this
// server, not the phone.
func TestAnExpiredProviderTokenIsRenewedAndThePushSentAgain(t *testing.T) {
	fake := apnstest.New(t)
	fake.Answer(token(1), apnstest.Answer{Status: http.StatusForbidden, Reason: "ExpiredProviderToken"})
	client := fake.Client(t, quiet())
	tickets, err := client.Send(context.Background(), []pushsend.Message{{To: token(1), Title: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	pushes := fake.Pushes()
	if len(tickets) != 1 || !tickets[0].Accepted || len(pushes) != 2 {
		t.Fatalf("tickets %+v after %d pushes", tickets, len(pushes))
	}
	if pushes[0].Header.Get("authorization") == pushes[1].Header.Get("authorization") {
		t.Error("the push was sent again with the token Apple refused")
	}
	if pushes[0].Header.Get("apns-id") != pushes[1].Header.Get("apns-id") {
		t.Error("the second try is a new push to Apple's tools; it is the same one")
	}
	// Refused again with the new one: not renewed in a loop, tried later.
	fake.Answer(token(2), apnstest.Answer{Status: http.StatusForbidden, Reason: "ExpiredProviderToken"}, apnstest.Answer{Status: http.StatusForbidden, Reason: "ExpiredProviderToken"})
	tickets, _ = client.Send(context.Background(), []pushsend.Message{{To: token(2), Title: "x"}})
	if len(tickets) != 1 || tickets[0].Accepted || tickets[0].Gone || len(fake.Pushes()) != 4 {
		t.Errorf("refused twice: %+v after %d pushes", tickets, len(fake.Pushes()))
	}
}

// A batch goes over parallel streams of one connection and every message
// gets its own ticket, in the messages' order.
func TestABatchAnswersEachMessageInOrder(t *testing.T) {
	fake := apnstest.New(t)
	messages := make([]pushsend.Message, pushsend.MaxBatch)
	for i := range messages {
		messages[i] = pushsend.Message{To: token(i), Title: "x"}
		if i%10 == 3 {
			fake.Answer(token(i), apnstest.Answer{Status: http.StatusGone, Reason: "Unregistered"})
		}
	}
	tickets, err := fake.Client(t, quiet()).Send(context.Background(), messages)
	if err != nil || len(tickets) != len(messages) {
		t.Fatalf("%d tickets, %v", len(tickets), err)
	}
	sentAs := map[string]string{}
	for _, push := range fake.Pushes() {
		sentAs[push.Header.Get("apns-id")] = push.Token
	}
	for i, ticket := range tickets {
		if ticket.Gone != (i%10 == 3) || ticket.Accepted == ticket.Gone {
			t.Errorf("message %d: %+v", i, ticket)
		}
		if sentAs[ticket.Reference] != token(i) {
			t.Errorf("ticket %d answers for %q", i, sentAs[ticket.Reference])
		}
	}
	if _, err := fake.Client(t, quiet()).Send(context.Background(), make([]pushsend.Message, pushsend.MaxBatch+1)); err == nil {
		t.Error("a batch over the limit was taken")
	}
}

// A token that is not Apple's shape is never sent (it is gone: it can never
// work), and a service that cannot be reached is tried again later.
func TestWhatCannotBeSentIsDecidedWithoutApple(t *testing.T) {
	fake := apnstest.New(t)
	client := fake.Client(t, quiet())
	tickets, _ := client.Send(context.Background(), []pushsend.Message{{To: "ExponentPushToken[x]", Title: "x"}, {To: "../../x", Title: "x"}})
	for i, ticket := range tickets {
		if !ticket.Gone || ticket.Accepted {
			t.Errorf("token %d: %+v", i, ticket)
		}
	}
	if len(fake.Pushes()) != 0 {
		t.Error("a token that is not Apple's was sent")
	}
	tooBig := pushsend.Message{To: token(1), Title: strings.Repeat("x", 5000)}
	if tickets, _ := client.Send(context.Background(), []pushsend.Message{tooBig}); tickets[0].Accepted || tickets[0].Retry || tickets[0].Gone {
		t.Errorf("a push over Apple's 4 KB: %+v", tickets[0])
	}
	fake.Close()
	tickets, err := client.Send(context.Background(), []pushsend.Message{{To: token(1), Title: "x"}})
	if err != nil || !tickets[0].Retry || tickets[0].Gone {
		t.Errorf("Apple unreachable: %+v, %v", tickets, err)
	}
}

// The production transport speaks HTTP/2 and nothing else: a server that
// offers only HTTP/1.1 is not spoken to.
func TestTheTransportIsHTTP2Only(t *testing.T) {
	reached := false
	old := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { reached = true }))
	old.EnableHTTP2 = false
	old.StartTLS()
	defer old.Close()
	roots := x509.NewCertPool()
	roots.AddCert(old.Certificate())
	request, _ := http.NewRequest(http.MethodPost, old.URL+"/3/device/"+token(1), strings.NewReader("{}"))
	if response, err := apns.NewHTTPClient(roots).Do(request); err == nil {
		response.Body.Close()
		t.Errorf("an HTTP/1.1 server answered HTTP/%d", response.ProtoMajor)
	}
	if reached {
		t.Error("the request reached a server that does not speak HTTP/2")
	}
}

// The environments name Apple's two hosts, and nothing else is taken.
func TestEachEnvironmentIsAppleHost(t *testing.T) {
	if got, err := apns.URLFor("production"); err != nil || got != "https://api.push.apple.com" {
		t.Errorf("production = %q, %v", got, err)
	}
	if got, err := apns.URLFor("sandbox"); err != nil || got != "https://api.sandbox.push.apple.com" {
		t.Errorf("sandbox = %q, %v", got, err)
	}
	for _, bad := range []string{"", "Production", "development", "prod"} {
		if _, err := apns.URLFor(bad); err == nil {
			t.Errorf("%q was taken", bad)
		}
	}
}
