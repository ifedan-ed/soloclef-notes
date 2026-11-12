package artifacts

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

var revisedAt = time.Date(2026, 3, 4, 5, 6, 8, 250_000_000, time.UTC)

// deckManifest is a studio whose artifact 31 is a deck of four cards at
// generation 3, with a preview beside its primary file.
func deckManifest(t *testing.T) *studio {
	t.Helper()
	s := newStudio(t)
	s.put(t, deckStorageKey, []byte(deckOf(4)))
	s.manifest = &ManifestRow{
		ID: 31, DocumentID: 310, Title: "Cell biology — “basics”", Format: "flashcards", Generation: 3,
		UpdatedAt: revisedAt, Markdown: "# Cell biology\n",
		Files: []StoredFile{
			{ID: 88, Role: "primary", Filename: "deck.json", MimeType: "application/json", Size: 512,
				Backend: "local", Key: deckStorageKey, Checksum: inventedChecksum},
			{ID: 89, Role: "preview", Filename: "deck.pdf", MimeType: "", Size: 2048,
				Backend: "local", Key: "artifacts/7/31/preview/x.pdf", Checksum: inventedChecksum},
		},
	}
	return s
}

func TestTheManifestInTheShapeThePanelReads(t *testing.T) {
	s := deckManifest(t)
	s.manifest.Legacy = []byte(`{"kind": "podcast", "id": 7, "extra": "dropped"}`)
	s.manifest.FlashcardState = []byte(`{"generation": 3, "marks": {"2": "good"}, "order": [3, 1, 0, 2]}`)
	got := s.get(t, manifestURL, nil)

	const want = `{"artifact_id":31,"document_id":310,"title":"Cell biology — “basics”","format":"flashcards",` +
		`"generation":3,"markdown_representation":"# Cell biology\n","files":[` +
		`{"file_id":88,"role":"primary","filename":"deck.json","mime_type":"application/json","size_bytes":512,` +
		`"content_url":"/api/v1/artifacts/31/files/88/content"},` +
		`{"file_id":89,"role":"preview","filename":"deck.pdf","mime_type":"application/octet-stream","size_bytes":2048,` +
		`"content_url":"/api/v1/artifacts/31/files/89/content"}],` +
		`"updated_at":"2026-03-04T05:06:08.250000+00:00","legacy":{"kind":"podcast","id":7},` +
		`"flashcard_study_state":{"generation":3,"marks":{"2":"good"},"order":[3,1,0,2]}}`
	if got.status != 200 || string(got.raw) != want {
		t.Fatalf("status %d\n got  %s\n want %s", got.status, got.raw, want)
	}
	if got.header.Get("Content-Type") != "application/json" || got.header.Get("Cache-Control") != "private, no-cache" {
		t.Errorf("headers %v", got.header)
	}
	// Where a file is stored is not the panel's business.
	for _, private := range []string{deckStorageKey, inventedChecksum, "storage", "local"} {
		if strings.Contains(string(got.raw), private) {
			t.Errorf("the manifest carries %q", private)
		}
	}
}

func TestTheStudyStateAManifestCarries(t *testing.T) {
	for name, test := range map[string]struct {
		arrange func(s *studio)
		// want is the manifest's last key and value.
		want string
	}{
		"a deck nobody has studied: the deck's own order": {
			func(*studio) {},
			`"flashcard_study_state":{"generation":3,"marks":{},"order":[0,1,2,3]}}`},
		"a state from an older generation is not shown": {
			func(s *studio) {
				s.manifest.FlashcardState = []byte(`{"generation": 2, "marks": {"0": "good"}, "order": [0, 1, 2, 3]}`)
			},
			`"flashcard_study_state":{"generation":3,"marks":{},"order":[0,1,2,3]}}`},
		"a state that does not fit the deck is not shown": {
			func(s *studio) {
				s.manifest.FlashcardState = []byte(`{"generation": 3, "marks": {}, "order": [0, 1, 2]}`)
			},
			`"flashcard_study_state":{"generation":3,"marks":{},"order":[0,1,2,3]}}`},
		"a quiz part way through": {
			func(s *studio) {
				s.manifest.Format = "quiz"
				s.put(t, deckStorageKey, []byte(quizOf(3, 1, 0, 1, 2)))
				s.manifest.QuizState = []byte(`{"generation": 3, "mode": "all", "active_question_indices": [0, 1, 2, 3, 4], "answers": {"0": 3}, "skipped_question_indices": [1]}`)
			},
			`"quiz_state":{"generation":3,"mode":"all","active_question_indices":[0,1,2,3,4],"answers":{"0":3},"skipped_question_indices":[1]}}`},
		"a quiz nobody has started": {
			func(s *studio) {
				s.manifest.Format = "quiz"
				s.put(t, deckStorageKey, []byte(quizOf(3, 1, 0, 1, 2)))
			},
			`"quiz_state":{"generation":3,"mode":"all","active_question_indices":[0,1,2,3,4],"answers":{},"skipped_question_indices":[]}}`},
		"a document has no study state, and its file is not read": {
			func(s *studio) {
				s.manifest.Format = "pdf"
				s.manifest.Files[0].Key = "artifacts/7/31/primary/not-there.pdf"
			},
			`"updated_at":"2026-03-04T05:06:08.250000+00:00"}`},
	} {
		t.Run(name, func(t *testing.T) {
			s := deckManifest(t)
			test.arrange(s)
			got := s.get(t, manifestURL, nil)
			if got.status != 200 || !strings.HasSuffix(string(got.raw), test.want) {
				t.Errorf("status %d: %s", got.status, got.raw)
			}
			if strings.Contains(s.log.String(), "level=WARN") || strings.Contains(s.log.String(), "level=ERROR") {
				t.Errorf("logged: %s", s.log)
			}
		})
	}
}

// A deck that cannot be read can still be named, downloaded and deleted, so
// its manifest is still answered -- and the server says why in its log.
func TestADeckThatCannotBeReadStillHasAManifest(t *testing.T) {
	for name, arrange := range map[string]func(s *studio){
		"the file is not a deck":       func(s *studio) { s.put(t, deckStorageKey, []byte(`not a deck`)) },
		"the file is gone":             func(s *studio) { s.manifest.Files[0].Key = "artifacts/7/31/primary/gone.json" },
		"there is no primary file":     func(s *studio) { s.manifest.Files = s.manifest.Files[1:] },
		"the file is in another store": func(s *studio) { s.manifest.Files[0].Backend = "s3" },
	} {
		t.Run(name, func(t *testing.T) {
			s := deckManifest(t)
			arrange(s)
			got := s.get(t, manifestURL, nil)
			if got.status != 200 ||
				!strings.HasSuffix(string(got.raw), `"flashcard_study_state":{"generation":3,"marks":{},"order":[]}}`) {
				t.Fatalf("status %d: %s", got.status, got.raw)
			}
			if !strings.Contains(s.log.String(), "level=WARN") ||
				!strings.Contains(s.log.String(), "study file could not be read") {
				t.Errorf("nothing was logged: %s", s.log)
			}
		})
	}
}

// The defect this guards against: a deck or quiz whose file could not be read
// *this once* was given the state of somebody who had never started, so the
// viewer -- which fetches the file itself, and usually gets it -- showed
// their progress as gone while it sat untouched in the row. The stored state
// is now sent, checked against the size it implies, in the shape the viewer
// already validates against the file it loads.
func TestProgressIsNotShownAsLostWhenTheStudyFileCannotBeRead(t *testing.T) {
	unreadable := func(s *studio) { s.manifest.Files[0].Key = "artifacts/7/31/primary/gone.json" }
	for name, test := range map[string]struct {
		arrange func(s *studio)
		want    string
	}{
		"a deck part way through, shuffled": {
			func(s *studio) {
				s.manifest.FlashcardState = []byte(`{"generation": 3, "marks": {"2": "good", "0": "again"}, "order": [3, 1, 0, 2]}`)
			},
			`"flashcard_study_state":{"generation":3,"marks":{"0":"again","2":"good"},"order":[3,1,0,2]}}`},
		"a deck's marks on cards its order does not have are still dropped": {
			func(s *studio) {
				s.manifest.FlashcardState = []byte(`{"generation": 3, "marks": {"1": "good", "7": "good", "0": "perhaps"}, "order": [1, 0]}`)
			},
			`"flashcard_study_state":{"generation":3,"marks":{"1":"good"},"order":[1,0]}}`},
		"a deck's state from an older generation is still not shown": {
			func(s *studio) {
				s.manifest.FlashcardState = []byte(`{"generation": 2, "marks": {"0": "good"}, "order": [0, 1, 2, 3]}`)
			},
			`"flashcard_study_state":{"generation":3,"marks":{},"order":[0,1,2,3]}}`},
		"a deck's order that is not an order is still not shown": {
			func(s *studio) {
				s.manifest.FlashcardState = []byte(`{"generation": 3, "marks": {"0": "good"}, "order": [0, 0, 2]}`)
			},
			`"flashcard_study_state":{"generation":3,"marks":{},"order":[0,1,2]}}`},
		"an order longer than any deck is not a deck's": {
			func(s *studio) {
				s.manifest.FlashcardState = []byte(`{"generation": 3, "marks": {}, "order": [` +
					strings.TrimSuffix(strings.Repeat("0,", maxCards+1), ",") + `]}`)
			},
			`"flashcard_study_state":{"generation":3,"marks":{},"order":[]}}`},
		"a quiz part way through": {
			func(s *studio) {
				s.manifest.Format = "quiz"
				s.manifest.QuizState = []byte(`{"generation": 3, "mode": "all", "active_question_indices": [0, 1, 2, 3, 4], "answers": {"0": 3, "9": 1}, "skipped_question_indices": [1]}`)
			},
			`"quiz_state":{"generation":3,"mode":"all","active_question_indices":[0,1,2,3,4],"answers":{"0":3},"skipped_question_indices":[1]}}`},
		"a retake of the missed questions, the earlier answers kept": {
			func(s *studio) {
				s.manifest.Format = "quiz"
				s.manifest.QuizState = []byte(`{"generation": 3, "mode": "missed", "active_question_indices": [1, 3], "answers": {"0": 3, "4": 2, "3": 0, "x": 1}, "skipped_question_indices": [1]}`)
			},
			`"quiz_state":{"generation":3,"mode":"missed","active_question_indices":[1,3],"answers":{"0":3,"3":0,"4":2},"skipped_question_indices":[1]}}`},
		"a quiz state naming a question past the format's limit is not shown": {
			func(s *studio) {
				s.manifest.Format = "quiz"
				s.manifest.QuizState = []byte(`{"generation": 3, "mode": "missed", "active_question_indices": [1, 4000000000], "answers": {}, "skipped_question_indices": []}`)
			},
			`"quiz_state":{"generation":3,"mode":"all","active_question_indices":[],"answers":{},"skipped_question_indices":[]}}`},
		"a quiz nobody has started": {
			func(s *studio) { s.manifest.Format = "quiz" },
			`"quiz_state":{"generation":3,"mode":"all","active_question_indices":[],"answers":{},"skipped_question_indices":[]}}`},
	} {
		t.Run(name, func(t *testing.T) {
			s := deckManifest(t)
			unreadable(s)
			test.arrange(s)
			got := s.get(t, manifestURL, nil)
			if got.status != 200 || !strings.HasSuffix(string(got.raw), test.want) {
				t.Errorf("status %d: %s\nwant: %s", got.status, got.raw, test.want)
			}
			if !strings.Contains(s.log.String(), "study file could not be read") {
				t.Errorf("nothing was logged: %s", s.log)
			}
		})
	}
}

// The tag is taken over what is sent, so anything in the manifest that
// changes, changes the tag -- the title and the file list included, which
// the tag used not to cover.
func TestTheManifestsTagFollowsEverythingInIt(t *testing.T) {
	s := deckManifest(t)
	first := s.get(t, manifestURL, nil)
	tag := first.header.Get("ETag")
	if len(tag) != 34 || tag[0] != '"' || tag[33] != '"' {
		t.Fatalf("tag %q", tag)
	}

	unchanged := s.get(t, manifestURL, http.Header{"If-None-Match": {tag}})
	if unchanged.status != 304 || len(unchanged.raw) != 0 ||
		unchanged.header.Get("ETag") != tag || unchanged.header.Get("Cache-Control") != "private, no-cache" {
		t.Errorf("status %d, %d bytes, headers %v", unchanged.status, len(unchanged.raw), unchanged.header)
	}
	// As HTTP compares them: weakened by a proxy, or one of several.
	for _, header := range []string{"W/" + tag, `"something-older", ` + tag} {
		if got := s.get(t, manifestURL, http.Header{"If-None-Match": {header}}); got.status != 304 {
			t.Errorf("If-None-Match %s: status %d", header, got.status)
		}
	}

	for name, change := range map[string]func(s *studio){
		"the title":       func(s *studio) { s.manifest.Title = "Cell biology, revised" },
		"the generation":  func(s *studio) { s.manifest.Generation = 4 },
		"the Markdown":    func(s *studio) { s.manifest.Markdown = "# Cells\n" },
		"a file":          func(s *studio) { s.manifest.Files[1].Size = 4096 },
		"the file list":   func(s *studio) { s.manifest.Files = s.manifest.Files[:1] },
		"the legacy link": func(s *studio) { s.manifest.Legacy = []byte(`{"kind": "video", "id": 3}`) },
		"a card marked": func(s *studio) {
			s.manifest.FlashcardState = []byte(`{"generation": 3, "marks": {"0": "good"}, "order": [0, 1, 2, 3]}`)
		},
		"the deck itself": func(s *studio) { s.put(t, deckStorageKey, []byte(deckOf(6))) },
	} {
		t.Run(name, func(t *testing.T) {
			s := deckManifest(t)
			change(s)
			got := s.get(t, manifestURL, http.Header{"If-None-Match": {tag}})
			if got.status != 200 || got.header.Get("ETag") == tag || len(got.raw) == 0 {
				t.Errorf("status %d, tag %s", got.status, got.header.Get("ETag"))
			}
		})
	}
}

func TestAManifestThatCannotBeAnswered(t *testing.T) {
	s := newStudio(t)
	if got := s.get(t, manifestURL, nil); got.status != 404 || got.message(t) != "Artifact not found" {
		t.Errorf("no artifact: status %d, %s", got.status, got.raw)
	}
	s.databaseDown = true
	if got := s.get(t, manifestURL, nil); got.status != 500 ||
		strings.Contains(string(got.raw), "unreachable") || got.header.Get("ETag") != "" {
		t.Errorf("database down: status %d, %s", got.status, got.raw)
	}
}
