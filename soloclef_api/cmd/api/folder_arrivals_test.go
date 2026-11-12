package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/eventstream"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/folders"
)

type publishedEvents struct{ events []eventstream.Event }

func (p *publishedEvents) Publish(_ context.Context, event eventstream.Event) (string, error) {
	p.events = append(p.events, event)
	return "1-0", nil
}

// An arrival becomes one document.entered_folder event, owned by the
// account the document belongs to and carrying every field of the arrival.
func TestAFolderArrivalIsPublishedForTheDocumentsOwner(t *testing.T) {
	const owner = "11111111-2222-4333-8444-555555555555"
	previous, connector := int64(3), int64(802)
	published := &publishedEvents{}
	err := folderArrivals{events: published}.DocumentEnteredFolder(context.Background(), folders.Arrival{
		UserID: owner, DocumentID: 41, FolderID: 7, PreviousFolderID: &previous,
		DocumentType: "FILE", Title: "Tide tables", ConnectorID: &connector,
	}, time.Date(2026, 11, 11, 9, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(published.events) != 1 {
		t.Fatalf("%d events were published", len(published.events))
	}
	event := published.events[0]
	if event.UserID != owner {
		t.Errorf("the event belongs to %q", event.UserID)
	}
	fields, err := event.Encode()
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(fields)
	for _, piece := range []string{eventstream.DocumentEnteredFolderType, `\"document_id\":41`, `\"folder_id\":7`,
		`\"previous_folder_id\":3`, `\"connector_id\":802`, `Tide tables`} {
		if !strings.Contains(string(encoded), piece) {
			t.Errorf("the event does not carry %s: %s", piece, encoded)
		}
	}
}
