package broker

import (
	"encoding/json"
	"testing"
)

// Kombu stamps a restored message so a second delivery is visible to whoever
// receives it; a message that came back without the stamp would be
// indistinguishable from a first delivery.
func TestMarkRedeliveredSetsBothFlagsKombuSets(t *testing.T) {
	raw := []byte(`{"body":"W10=","headers":{"task":"t","retries":0},` +
		`"properties":{"priority":0,"delivery_info":{"exchange":"surfsense","routing_key":"surfsense"}}}`)
	marked, err := markRedelivered(raw)
	if err != nil {
		t.Fatalf("markRedelivered: %v", err)
	}

	var payload struct {
		Headers struct {
			Task        string `json:"task"`
			Redelivered bool   `json:"redelivered"`
		} `json:"headers"`
		Properties struct {
			DeliveryInfo struct {
				Redelivered bool   `json:"redelivered"`
				RoutingKey  string `json:"routing_key"`
			} `json:"delivery_info"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(marked, &payload); err != nil {
		t.Fatalf("the marked payload is not JSON: %v", err)
	}
	if !payload.Headers.Redelivered || !payload.Properties.DeliveryInfo.Redelivered {
		t.Error("both headers.redelivered and properties.delivery_info.redelivered must be set")
	}
	if payload.Headers.Task != "t" || payload.Properties.DeliveryInfo.RoutingKey != "surfsense" {
		t.Error("marking must leave the rest of the envelope alone")
	}
}

// Kombu swallows a payload it cannot stamp and restores it anyway. We do not
// guess: an envelope without the shape to stamp is an error the caller sees.
func TestMarkRedeliveredRefusesWhatItCannotStamp(t *testing.T) {
	for _, raw := range []string{`[]`, `{`, `{"headers":"not an object"}`} {
		if _, err := markRedelivered([]byte(raw)); err == nil {
			t.Errorf("markRedelivered(%s) accepted an envelope it cannot stamp", raw)
		}
	}
}

func TestRestoreKeyUsesTheQueueAndPriorityKombuPublishedTo(t *testing.T) {
	key, err := restoreKey("surfsense.heavy", 0)
	if err != nil || key != "surfsense.heavy" {
		t.Fatalf("restoreKey = %q, %v", key, err)
	}
	if key, err := restoreKey("surfsense", 6); err != nil || key != "surfsense\x06\x166" {
		t.Fatalf("restoreKey at priority 6 = %q, %v", key, err)
	}
	if _, err := restoreKey("", 0); err == nil {
		t.Error("a message with no routing key has nowhere to be restored to and must not be guessed at")
	}
}
