package eventstream

import (
	"encoding/json"
	"errors"
	"regexp"
	"testing"
	"time"
)

// The entry's bytes are a contract with whatever reads the stream: the field
// names, their order, and the owner carried as user_id.
const (
	ada             = "0b9f0a52-6f0c-4d4e-9d0b-5a1f6f0f7f0a"
	wantArrival     = `{"event_type":"document.entered_folder","payload":{"document_id":42,"folder_id":7,"previous_folder_id":5,"document_type":"FILE","title":"Q3 report.pdf","connector_id":12},"user_id":"` + ada + `","event_id":"8a1a46532b3e447b9b5cff45459f1dfa","occurred_at":"2026-11-08T06:07:41.077412Z"}`
	wantWholeSecond = `{"event_type":"x.happened","payload":{},"user_id":"` + ada + `","event_id":"00000000000000000000000000000000","occurred_at":"2026-11-08T06:07:41Z"}`
)

func TestAnArrivalIsWrittenForItsOwner(t *testing.T) {
	previous := int64(5)
	connector := int64(12)
	event, err := DocumentEnteredFolderEvent(ada, DocumentEnteredFolder{
		DocumentID:       42,
		FolderID:         7,
		PreviousFolderID: &previous,
		DocumentType:     "FILE",
		Title:            "Q3 report.pdf",
		ConnectorID:      &connector,
	}, time.Date(2026, 11, 8, 6, 7, 41, 77412000, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	event.EventID = "8a1a46532b3e447b9b5cff45459f1dfa"

	fields, err := event.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if fields[Field] != wantArrival {
		t.Errorf("the entry is\n got %s\nwant %s", fields[Field], wantArrival)
	}
}

// An event with no owner could be acted on for nobody, or for everybody. It
// is refused before it is stamped.
func TestAnEventWithNoOwnerIsRefused(t *testing.T) {
	if _, err := NewEvent("x.happened", map[string]any{}, "", time.Now()); !errors.Is(err, ErrNoOwner) {
		t.Fatalf("got %v, want ErrNoOwner", err)
	}
}

// No fraction is written when there is none.
func TestATimeOnAWholeSecondCarriesNoFraction(t *testing.T) {
	event, err := NewEvent("x.happened", map[string]any{}, ada, time.Date(2026, 11, 8, 6, 7, 41, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	event.EventID = "00000000000000000000000000000000"

	fields, err := event.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if fields[Field] != wantWholeSecond {
		t.Errorf("the entry is\n got %s\nwant %s", fields[Field], wantWholeSecond)
	}
}

// A non-UTC clock must still write the instant in UTC, or a
// subscriber's occurred_at would be an hour out.
func TestATimeInAnotherZoneIsWrittenInUTC(t *testing.T) {
	zone := time.FixedZone("plus-two", 2*60*60)
	event, err := NewEvent("x.happened", map[string]any{}, ada, time.Date(2026, 11, 8, 8, 7, 41, 0, zone))
	if err != nil {
		t.Fatal(err)
	}
	if event.OccurredAt != "2026-11-08T06:07:41Z" {
		t.Errorf("occurred_at = %q", event.OccurredAt)
	}
}

func TestTheEventIdIsAUuid4Hex(t *testing.T) {
	shape := regexp.MustCompile(`^[0-9a-f]{12}4[0-9a-f]{3}[89ab][0-9a-f]{15}$`)
	seen := map[string]bool{}
	for range 100 {
		event, err := NewEvent("x.happened", map[string]any{}, ada, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if !shape.MatchString(event.EventID) {
			t.Fatalf("event_id %q is not uuid4().hex", event.EventID)
		}
		if seen[event.EventID] {
			t.Fatalf("event_id %q repeated", event.EventID)
		}
		seen[event.EventID] = true
	}
}

// The entry holds exactly one field.
func TestTheEntryHoldsOneField(t *testing.T) {
	event, err := NewEvent("x.happened", map[string]any{"k": "v"}, ada, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	fields, err := event.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 {
		t.Fatalf("fields = %v", fields)
	}
	var back map[string]any
	if err := json.Unmarshal([]byte(fields[Field]), &back); err != nil {
		t.Fatal(err)
	}
	if back["event_type"] != "x.happened" {
		t.Errorf("event_type = %v", back["event_type"])
	}
}
