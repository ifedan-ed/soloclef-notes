package uploads

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestWithAVisionModelTheSwitchIsStoredOnEveryDocument(t *testing.T) {
	s := newSite(t)
	s.vision.refusal = ""

	got := s.upload(field("parser", "local"), field("use_vision_llm", "true"),
		file("scan.pdf", "%PDF-1.7 a scan"), file("notes.md", "# Notes\n"))
	if got.status != http.StatusOK {
		t.Fatalf("status %d: %s", got.status, got.message)
	}
	for _, name := range []string{"scan.pdf", "notes.md"} {
		if held := s.titled(name); held == nil || !held.vision {
			t.Errorf("%s was recorded as %+v", name, held)
		}
	}
	// The caller was asked about, and only the caller.
	if !reflect.DeepEqual(s.vision.asked, []string{callerID}) {
		t.Errorf("asked about %v", s.vision.asked)
	}

	// Uploaded again with different bytes and the switch off: the row says
	// off, so the next index does not describe its pictures.
	s.titled("scan.pdf").state = "ready"
	if got := s.upload(field("use_vision_llm", "false"), file("scan.pdf", "%PDF-1.7 a new scan")); got.status != http.StatusOK {
		t.Fatalf("status %d: %s", got.status, got.message)
	}
	if held := s.titled("scan.pdf"); held.vision {
		t.Error("a re-upload with vision off kept it on")
	}
}

func TestWithVisionOnAPictureIsTakenWhateverTheParserReads(t *testing.T) {
	s := newSite(t)
	s.vision.refusal = ""
	// The local parser does not read .png; the vision model does.
	got := s.upload(field("parser", "local"), field("use_vision_llm", "true"), file("scan.png", "\x89PNG picture"))
	if got.status != http.StatusOK || s.titled("scan.png") == nil {
		t.Fatalf("status %d: %s", got.status, got.message)
	}
	// With vision off the parser's list applies, as it always has.
	got = s.upload(field("parser", "local"), field("use_vision_llm", "false"), file("photo.jpg", "\xff\xd8\xff"))
	if got.status != http.StatusBadRequest || !strings.Contains(got.message, ".jpg is not read by this parser") {
		t.Fatalf("status %d: %s", got.status, got.message)
	}
}

func TestTheVisionCheckIsNotMadeWhenTheSwitchIsOff(t *testing.T) {
	s := newSite(t)
	if got := s.upload(file("a.pdf", "%PDF")); got.status != http.StatusOK {
		t.Fatalf("status %d: %s", got.status, got.message)
	}
	if len(s.vision.asked) != 0 {
		t.Errorf("the account's models were read for an upload that did not ask: %v", s.vision.asked)
	}
}

func TestARefusalRefusesTheSwitchInWords(t *testing.T) {
	s := newSite(t)
	s.vision.refusal = "No vision model is set for this account or on this server"
	got := s.upload(field("use_vision_llm", "true"), file("a.pdf", "%PDF"))
	if got.status != http.StatusBadRequest || !strings.Contains(got.message, "No vision model is set") || len(s.stored()) != 0 {
		t.Fatalf("status %d: %s; %d stored", got.status, got.message, len(s.stored()))
	}
}
