package eventstream

import (
	"encoding/json"
	"regexp"
	"testing"
	"time"
)

// The two strings below were printed by app.event_bus.stream.wire.encode for
// the same events, so a diff here is this tier drifting from that module.
const (
	pythonArrival     = `{"event_type":"document.entered_folder","payload":{"document_id":42,"folder_id":7,"previous_folder_id":5,"document_type":"FILE","title":"Q3 report.pdf","connector_id":12,"created_by_id":"7f0a"},"workspace_id":3,"event_id":"8a1a46532b3e447b9b5cff45459f1dfa","occurred_at":"2026-11-08T06:07:41.077412Z"}`
	pythonWholeSecond = `{"event_type":"x.happened","payload":{},"workspace_id":1,"event_id":"00000000000000000000000000000000","occurred_at":"2026-11-08T06:07:41Z"}`
)

func TestAnArrivalIsTheSameBytesPythonWrites(t *testing.T) {
	previous := int64(5)
	connector := int64(12)
	author := "7f0a"
	event, err := DocumentEnteredFolderEvent(3, DocumentEnteredFolder{
		DocumentID:       42,
		FolderID:         7,
		PreviousFolderID: &previous,
		DocumentType:     "FILE",
		Title:            "Q3 report.pdf",
		ConnectorID:      &connector,
		CreatedByID:      &author,
	}, time.Date(2026, 11, 8, 6, 7, 41, 77412000, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	event.EventID = "8a1a46532b3e447b9b5cff45459f1dfa"

	fields, err := event.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if fields[Field] != pythonArrival {
		t.Errorf("entry differs from the Python one\n got %s\nwant %s", fields[Field], pythonArrival)
	}
}

// Pydantic writes no fraction when there is none, so neither does this.
func TestATimeOnAWholeSecondCarriesNoFraction(t *testing.T) {
	event, err := NewEvent("x.happened", map[string]any{}, 1, time.Date(2026, 11, 8, 6, 7, 41, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	event.EventID = "00000000000000000000000000000000"

	fields, err := event.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if fields[Field] != pythonWholeSecond {
		t.Errorf("entry differs from the Python one\n got %s\nwant %s", fields[Field], pythonWholeSecond)
	}
}

// A non-UTC clock must still write the instant as pydantic writes it, or a
// subscriber's occurred_at would be an hour out.
func TestATimeInAnotherZoneIsWrittenInUTC(t *testing.T) {
	zone := time.FixedZone("plus-two", 2*60*60)
	event, err := NewEvent("x.happened", map[string]any{}, 1, time.Date(2026, 11, 8, 8, 7, 41, 0, zone))
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
		event, err := NewEvent("x.happened", map[string]any{}, 1, time.Now())
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

// The entry holds exactly one field, as wire.encode's map does.
func TestTheEntryHoldsOneField(t *testing.T) {
	event, err := NewEvent("x.happened", map[string]any{"k": "v"}, 1, time.Now())
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
