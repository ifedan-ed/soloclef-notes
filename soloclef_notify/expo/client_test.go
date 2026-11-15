package expo_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_notify/expo"
	"git.danvics.com/danvics/soloclef/soloclef_notify/expo/expotest"
	"git.danvics.com/danvics/soloclef/soloclef_notify/pushsend"
)

func client(fake *expotest.Server, token string) expo.Client {
	return expo.Client{BaseURL: fake.URL, AccessToken: token, HTTP: &http.Client{Timeout: 5 * time.Second}}
}

// Each message gets its own ticket, in order: an accepted one an id, a
// token that is gone its error, so the caller can delete exactly that token.
func TestTicketsComeBackInTheMessagesOrder(t *testing.T) {
	fake := expotest.New()
	defer fake.Close()
	fake.Gone("ExponentPushToken[gone]")
	badge := 3
	tickets, err := client(fake, "").Send(context.Background(), []pushsend.Message{
		{To: "ExponentPushToken[a]", Title: "Slide deck ready", Badge: &badge, ChannelID: "soloclef", Data: map[string]any{"path": "/inbox"},
			TTL: 24 * time.Hour, ThreadID: "output", CollapseID: "source:failed"},
		{To: "ExponentPushToken[gone]", Title: "Slide deck ready"},
		{To: "ExponentPushToken[b]", Title: "Slide deck ready"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tickets) != 3 || !tickets[0].Accepted || tickets[0].ReceiptID == "" || !tickets[2].Accepted ||
		tickets[1].Accepted || !tickets[1].Gone || tickets[1].Error != expo.DeviceNotRegistered {
		t.Fatalf("tickets = %+v", tickets)
	}
	sent := fake.Messages()
	// The TTL goes in Expo's unit, seconds; Expo has no thread or collapse
	// id, so those are not sent.
	if sent[0].Badge == nil || *sent[0].Badge != 3 || sent[0].ChannelID != "soloclef" || sent[0].Data["path"] != "/inbox" || sent[0].TTL != 86400 || sent[1].TTL != 0 {
		t.Errorf("the message arrived as %+v", sent[0])
	}
}

// The access token is sent only when one is set: absent means Expo's push
// security is off, and nothing is sent in its place.
func TestTheAccessTokenIsSentOnlyWhenSet(t *testing.T) {
	fake := expotest.New()
	defer fake.Close()
	message := []pushsend.Message{{To: "ExponentPushToken[a]", Title: "x"}}
	if _, err := client(fake, "").Send(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if _, err := client(fake, "secret-token").Send(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if got := fake.Authorizations(); len(got) != 2 || got[0] != "" || got[1] != "Bearer secret-token" {
		t.Errorf("authorizations = %q", got)
	}
}

// A rate limit or a server error may pass and is ErrTransient; a request
// the service refuses as such is not, because it would be refused again.
func TestOnlyFailuresThatMayPassAreTransient(t *testing.T) {
	fake := expotest.New()
	defer fake.Close()
	message := []pushsend.Message{{To: "ExponentPushToken[a]", Title: "x"}}
	for _, status := range []int{http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable} {
		fake.FailNext(status)
		if _, err := client(fake, "").Send(context.Background(), message); !errors.Is(err, pushsend.ErrTransient) {
			t.Errorf("HTTP %d: %v, want transient", status, err)
		}
	}
	fake.FailNext(http.StatusBadRequest)
	if _, err := client(fake, "").Send(context.Background(), message); err == nil || errors.Is(err, pushsend.ErrTransient) {
		t.Errorf("HTTP 400: %v, want a lasting failure", err)
	}
	unreachable := expo.Client{BaseURL: "http://127.0.0.1:1", HTTP: &http.Client{Timeout: time.Second}}
	if _, err := unreachable.Send(context.Background(), message); !errors.Is(err, pushsend.ErrTransient) {
		t.Errorf("no connection: %v, want transient", err)
	}
}

func TestABatchOverTheLimitIsRefusedBeforeItIsSent(t *testing.T) {
	fake := expotest.New()
	defer fake.Close()
	messages := make([]pushsend.Message, pushsend.MaxBatch+1)
	if _, err := client(fake, "").Send(context.Background(), messages); err == nil {
		t.Fatal("101 messages were sent")
	}
	if len(fake.Requests()) != 0 {
		t.Error("the fake was called")
	}
}

func TestReceiptsNameTheTokensThatWentAway(t *testing.T) {
	fake := expotest.New()
	defer fake.Close()
	fake.GoneLater("ExponentPushToken[later]")
	c := client(fake, "")
	tickets, err := c.Send(context.Background(), []pushsend.Message{{To: "ExponentPushToken[a]"}, {To: "ExponentPushToken[later]"}})
	if err != nil {
		t.Fatal(err)
	}
	receipts, err := c.Receipts(context.Background(), []string{tickets[0].ReceiptID, tickets[1].ReceiptID, "ticket-unknown"})
	if err != nil {
		t.Fatal(err)
	}
	if first, later := receipts[tickets[0].ReceiptID], receipts[tickets[1].ReceiptID]; len(receipts) != 2 || first.Error != "" || first.Gone ||
		!later.Gone || later.Error != expo.DeviceNotRegistered {
		t.Errorf("receipts = %+v", receipts)
	}
}
