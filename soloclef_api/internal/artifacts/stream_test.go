package artifacts

import (
	"bytes"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
)

// video is a studio holding one 300 KB "video" file, row 88.
func videoStudio(t *testing.T) (*studio, []byte) {
	t.Helper()
	s := newStudio(t)
	content := bytesOf(300_000)
	s.put(t, videoStorageKey, content)
	s.file = &StoredFile{
		ID: 88, Role: "primary", Filename: "Tour of the cell.mp4", MimeType: "video/mp4",
		Size: 300_000, Backend: "local", Key: videoStorageKey, Checksum: inventedChecksum,
	}
	return s, content
}

func TestAFileIsStreamedWithEverythingAPlayerNeeds(t *testing.T) {
	s, content := videoStudio(t)
	got := s.get(t, fileURL, nil)

	if got.status != http.StatusOK || !bytes.Equal(got.raw, content) {
		t.Fatalf("status %d, %d bytes", got.status, len(got.raw))
	}
	for header, want := range map[string]string{
		"Content-Type":           "video/mp4",
		"Content-Length":         "300000",
		"Accept-Ranges":          "bytes",
		"ETag":                   `"` + inventedChecksum + `"`,
		"Cache-Control":          "private, max-age=31536000, immutable",
		"X-Content-Type-Options": "nosniff",
		"Content-Disposition":    `inline; filename="Tour of the cell.mp4"; filename*=UTF-8''Tour%20of%20the%20cell.mp4`,
		"X-Request-ID":           "req_test",
	} {
		if got.header.Get(header) != want {
			t.Errorf("%s: %q, want %q", header, got.header.Get(header), want)
		}
	}
}

func TestSeekingAFile(t *testing.T) {
	s, content := videoStudio(t)
	for name, test := range map[string]struct {
		header       http.Header
		status       int
		body         []byte
		contentRange string
	}{
		"the opening probe": {
			http.Header{"Range": {"bytes=0-"}}, 206, content, "bytes 0-299999/300000"},
		"a seek to the middle": {
			http.Header{"Range": {"bytes=150000-"}}, 206, content[150000:], "bytes 150000-299999/300000"},
		"one chunk": {
			http.Header{"Range": {"bytes=65536-131071"}}, 206, content[65536:131072], "bytes 65536-131071/300000"},
		"the index at the end of the file": {
			http.Header{"Range": {"bytes=-4096"}}, 206, content[295904:], "bytes 295904-299999/300000"},
		"past the end": {
			http.Header{"Range": {"bytes=300000-"}}, 416, nil, "bytes */300000"},
		"a range that is not one": {
			http.Header{"Range": {"bytes=soon"}}, 200, content, ""},
		"already cached": {
			http.Header{"If-None-Match": {`"` + inventedChecksum + `"`}}, 304, nil, ""},
		"resuming a file that has not changed": {
			http.Header{"Range": {"bytes=299990-"}, "If-Range": {`"` + inventedChecksum + `"`}},
			206, content[299990:], "bytes 299990-299999/300000"},
		"resuming one that has": {
			http.Header{"Range": {"bytes=299990-"}, "If-Range": {`"another-checksum"`}},
			200, content, ""},
	} {
		t.Run(name, func(t *testing.T) {
			got := s.get(t, fileURL, test.header)
			if got.status != test.status || !bytes.Equal(got.raw, test.body) {
				t.Fatalf("status %d with %d bytes, want %d with %d", got.status, len(got.raw), test.status, len(test.body))
			}
			if got.header.Get("Content-Range") != test.contentRange {
				t.Errorf("Content-Range %q, want %q", got.header.Get("Content-Range"), test.contentRange)
			}
			if test.body != nil && got.header.Get("Content-Length") != strconv.Itoa(len(test.body)) {
				t.Errorf("Content-Length %q for %d bytes", got.header.Get("Content-Length"), len(test.body))
			}
		})
	}
}

func TestAHeadOfAFileSendsItsHeadersOnly(t *testing.T) {
	s, _ := videoStudio(t)
	got := s.call(t, principal.MethodSession, http.MethodHead, fileURL, nil, "")
	if got.status != http.StatusOK || len(got.raw) != 0 ||
		got.header.Get("Content-Length") != "300000" || got.header.Get("Content-Type") != "video/mp4" {
		t.Errorf("status %d, %d bytes, headers %v", got.status, len(got.raw), got.header)
	}
}

// Only PDF and MP4 are shown in the page; anything else is saved, however
// harmless its type claims to be.
func TestWhatAFileIsServedAs(t *testing.T) {
	for name, test := range map[string]struct {
		filename, mimeType string
		contentType        string
		disposition        string
	}{
		"a PDF is shown":                     {"Plan.pdf", "application/pdf", "application/pdf", "inline"},
		"a deck is saved":                    {"deck.json", "application/json", "application/json", "attachment"},
		"an SVG is saved":                    {"logo.svg", "image/svg+xml", "image/svg+xml", "attachment"},
		"HTML is saved":                      {"page.html", "text/html", "text/html", "attachment"},
		"a row with no type is bytes, saved": {"blob", "", "application/octet-stream", "attachment"},
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := videoStudio(t)
			s.file.Filename, s.file.MimeType = test.filename, test.mimeType
			got := s.get(t, fileURL, nil)
			if got.status != 200 || got.header.Get("Content-Type") != test.contentType ||
				!strings.HasPrefix(got.header.Get("Content-Disposition"), test.disposition+";") {
				t.Errorf("status %d, %q, %q", got.status,
					got.header.Get("Content-Type"), got.header.Get("Content-Disposition"))
			}
		})
	}
}

// The length a client is told is the length of what is in the store. A row
// that disagrees with its file must not produce a response that promises one
// number of bytes and sends another.
func TestTheLengthSentIsTheStoredObjects(t *testing.T) {
	s, content := videoStudio(t)
	s.file.Size = 123
	got := s.get(t, fileURL, nil)
	if got.status != 200 || got.header.Get("Content-Length") != "300000" || !bytes.Equal(got.raw, content) {
		t.Errorf("status %d, Content-Length %q, %d bytes", got.status, got.header.Get("Content-Length"), len(got.raw))
	}
}

// What can go wrong with a file, and what each is answered with. None of
// them is a 200 that stops short.
func TestAFileThatCannotBeSent(t *testing.T) {
	for name, test := range map[string]struct {
		arrange func(s *studio)
		path    string
		status  int
		message string
		// logged is text the server's log must hold; "" means nothing is
		// logged as an error.
		logged string
	}{
		"no such file row": {
			func(s *studio) { s.file = nil }, fileURL, 404, "Artifact file not found", ""},
		"a row whose bytes are gone": {
			func(s *studio) { s.file.Key = "artifacts/7/31/primary/ffffffffffffffffffffffffffffffff.mp4" },
			fileURL, 404, "Artifact file not found", "missing from the file store"},
		"a row whose key climbs out of the store": {
			func(s *studio) { s.file.Key = "../../../etc/passwd" },
			fileURL, 404, "Artifact file not found", "missing from the file store"},
		"a row whose key is a directory": {
			func(s *studio) { s.file.Key = "artifacts/7/31" },
			fileURL, 404, "Artifact file not found", "missing from the file store"},
		"a row recorded in a backend this tier does not read": {
			func(s *studio) { s.file.Backend = "s3" },
			fileURL, 500, "An internal error occurred. Please try again or report this issue if it persists.",
			`not one this process reads: \"s3\" (it reads \"local\")`},
		"the database is down": {
			func(s *studio) { s.databaseDown = true },
			fileURL, 500, "An internal error occurred. Please try again or report this issue if it persists.",
			"the database is unreachable"},
		"a file id that is not a number": {
			func(*studio) {}, base + "/files/abc/content", 422, "", ""},
		"a file id larger than any id": {
			func(*studio) {}, base + "/files/99999999999999999999999/content", 422, "", ""},
		"an artifact id past what an integer holds": {
			func(*studio) {}, "/api/v1/artifacts/99999999999999999999999/files/88/content", 422, "", ""},
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := videoStudio(t)
			test.arrange(s)
			got := s.get(t, test.path, nil)
			if got.status != test.status {
				t.Fatalf("status %d, want %d: %s", got.status, test.status, got.raw)
			}
			if test.message != "" && got.message(t) != test.message {
				t.Errorf("told %q", got.message(t))
			}
			// An error is JSON in the usual envelope, never a file's headers.
			if got.header.Get("Content-Type") != "application/json" || got.header.Get("Accept-Ranges") != "" {
				t.Errorf("headers %v", got.header)
			}
			if test.logged == "" && strings.Contains(s.log.String(), "level=ERROR") {
				t.Errorf("logged an error: %s", s.log)
			}
			if !strings.Contains(s.log.String(), test.logged) {
				t.Errorf("the log does not say %q: %s", test.logged, s.log)
			}
			// What is logged for the operator is not what the caller is told.
			if strings.Contains(string(got.raw), "etc/passwd") || strings.Contains(string(got.raw), "unreachable") {
				t.Errorf("the answer carries server detail: %s", got.raw)
			}
		})
	}
}

func TestTheDownloadIsTheFileNamedAfterTheArtifact(t *testing.T) {
	s := newStudio(t)
	deck := []byte(deckOf(12))
	s.put(t, deckStorageKey, deck)
	s.download = &Download{
		Title: "Cell biology: the basics",
		File: &StoredFile{ID: 88, Role: "primary", Filename: "generated-deck.JSON", MimeType: "application/json",
			Backend: "local", Key: deckStorageKey, Checksum: inventedChecksum},
	}
	got := s.get(t, downloadURL, nil)

	if got.status != 200 || !bytes.Equal(got.raw, deck) {
		t.Fatalf("status %d, %d bytes", got.status, len(got.raw))
	}
	for header, want := range map[string]string{
		"Content-Type":           "application/json",
		"Content-Length":         strconv.Itoa(len(deck)),
		"Cache-Control":          "private, no-store",
		"X-Content-Type-Options": "nosniff",
		"Content-Disposition": `attachment; filename="Cell biology_ the basics.json"; ` +
			`filename*=UTF-8''Cell%20biology_%20the%20basics.json`,
	} {
		if got.header.Get(header) != want {
			t.Errorf("%s: %q, want %q", header, got.header.Get(header), want)
		}
	}

	// Even a type that may be shown in the page is an attachment here.
	s.download.File.MimeType, s.download.File.Filename = "application/pdf", "x.pdf"
	if got := s.get(t, downloadURL, nil); !strings.HasPrefix(got.header.Get("Content-Disposition"), "attachment;") {
		t.Errorf("a PDF download is %q", got.header.Get("Content-Disposition"))
	}
	// A large download can be resumed.
	if got := s.get(t, downloadURL, http.Header{"Range": {"bytes=10-19"}}); got.status != 206 ||
		!bytes.Equal(got.raw, deck[10:20]) {
		t.Errorf("a ranged download: status %d, %q", got.status, got.raw)
	}
}

func TestAnArtifactWithNoFileDownloadsAsItsMarkdown(t *testing.T) {
	s := newStudio(t)
	s.download = &Download{Title: "Réunion — notes", Markdown: "# Réunion\n\nDécisions prises.\n"}
	got := s.get(t, downloadURL, nil)

	if got.status != 200 || string(got.raw) != "# Réunion\n\nDécisions prises.\n" {
		t.Fatalf("status %d: %q", got.status, got.raw)
	}
	for header, want := range map[string]string{
		"Content-Type":           "text/markdown; charset=utf-8",
		"Content-Length":         strconv.Itoa(len("# Réunion\n\nDécisions prises.\n")),
		"Cache-Control":          "private, no-store",
		"X-Content-Type-Options": "nosniff",
		"Content-Disposition": `attachment; filename="Runion _ notes.md"; ` +
			`filename*=UTF-8''R%C3%A9union%20_%20notes.md`,
	} {
		if got.header.Get(header) != want {
			t.Errorf("%s: %q, want %q", header, got.header.Get(header), want)
		}
	}
}

func TestADownloadThatCannotBeSent(t *testing.T) {
	s := newStudio(t)
	if got := s.get(t, downloadURL, nil); got.status != 404 || got.message(t) != "Artifact not found" {
		t.Errorf("no artifact: status %d, %s", got.status, got.raw)
	}
	s.download = &Download{Title: "T", File: &StoredFile{ID: 88, Filename: "a.pdf", MimeType: "application/pdf",
		Backend: "local", Key: "artifacts/7/31/primary/gone.pdf", Checksum: inventedChecksum}}
	if got := s.get(t, downloadURL, nil); got.status != 404 || got.message(t) != "Artifact file not found" {
		t.Errorf("bytes gone: status %d, %s", got.status, got.raw)
	}
}

// slides is a video with three slides: one narrated out of the store, one
// rendered before narration was stored, one silent.
const threeSlides = `[` +
	`{"slide_number": 1, "title": "Opening"},` +
	`{"slide_number": 2, "title": "Membranes", "audio_storage_key": "` + audioStorageKey + `", "storage_backend": "local"},` +
	`{"slide_number": 3, "title": "Old", "audio_file": "/srv/render/tmp/slide-3.mp3"},` +
	`"not a slide",` +
	`{"slide_number": "4", "audio_storage_key": "` + audioStorageKey + `"}` +
	`]`

func slideStudio(t *testing.T) (*studio, []byte) {
	t.Helper()
	s := newStudio(t)
	audio := bytesOf(48_000)
	s.put(t, audioStorageKey, audio)
	s.slides = &SlideSet{Format: "video", Slides: []byte(threeSlides)}
	return s, audio
}

func TestASlidesNarrationIsStreamedAndCanBeScrubbed(t *testing.T) {
	s, audio := slideStudio(t)
	got := s.get(t, slideURL, nil)
	if got.status != 200 || !bytes.Equal(got.raw, audio) {
		t.Fatalf("status %d, %d bytes: %s", got.status, len(got.raw), s.log)
	}
	for header, want := range map[string]string{
		"Content-Type":           "audio/mpeg",
		"Content-Length":         "48000",
		"Accept-Ranges":          "bytes",
		"Cache-Control":          "private, no-cache",
		"X-Content-Type-Options": "nosniff",
		// Named for the slide; the storage key's own name is not sent.
		"Content-Disposition": `inline; filename="slide-2.mp3"; filename*=UTF-8''slide-2.mp3`,
	} {
		if got.header.Get(header) != want {
			t.Errorf("%s: %q, want %q", header, got.header.Get(header), want)
		}
	}

	// The route said Accept-Ranges and used to ignore Range.
	ranged := s.get(t, slideURL, http.Header{"Range": {"bytes=24000-"}})
	if ranged.status != 206 || !bytes.Equal(ranged.raw, audio[24000:]) ||
		ranged.header.Get("Content-Range") != "bytes 24000-47999/48000" {
		t.Errorf("status %d, %d bytes, %q", ranged.status, len(ranged.raw), ranged.header.Get("Content-Range"))
	}
	// And the tag it was given earns a 304.
	again := s.get(t, slideURL, http.Header{"If-None-Match": {got.header.Get("ETag")}})
	if got.header.Get("ETag") == "" || again.status != 304 || len(again.raw) != 0 {
		t.Errorf("tag %q, then status %d", got.header.Get("ETag"), again.status)
	}
}

func TestAWavNarrationIsServedAsWav(t *testing.T) {
	s := newStudio(t)
	const key = "video_presentations/7/31/slide-2-00112233445566778899aabbccddeeff.WAV"
	s.put(t, key, bytesOf(100))
	// No backend recorded on the slide: it was written before that was kept.
	s.slides = &SlideSet{Format: "video", Slides: []byte(`[{"slide_number": 2, "audio_storage_key": "` + key + `"}]`)}
	got := s.get(t, slideURL, nil)
	if got.status != 200 || got.header.Get("Content-Type") != "audio/wav" ||
		!strings.Contains(got.header.Get("Content-Disposition"), `filename="slide-2.wav"`) {
		t.Errorf("status %d, %q, %q", got.status, got.header.Get("Content-Type"), got.header.Get("Content-Disposition"))
	}
}

func TestASlideWithNoNarrationToSend(t *testing.T) {
	for name, test := range map[string]struct {
		arrange func(s *studio)
		path    string
		status  int
		message string
	}{
		"no such artifact": {func(s *studio) { s.slides = nil }, slideURL, 404, "Artifact not found"},
		"not a video":      {func(s *studio) { s.slides.Format = "flashcards" }, slideURL, 404, "Artifact is not a video"},
		"a silent slide":   {func(*studio) {}, base + "/slides/1/audio", 404, "Slide audio file not found"},
		"a slide whose audio was never moved into the store": {
			func(*studio) {}, base + "/slides/3/audio", 404, "Slide audio file not found"},
		"a slide whose number was stored as text has no number": {
			func(*studio) {}, base + "/slides/4/audio", 404, "Slide 4 not found"},
		"no such slide":          {func(*studio) {}, base + "/slides/9/audio", 404, "Slide 9 not found"},
		"a negative slide":       {func(*studio) {}, base + "/slides/-2/audio", 404, "Slide -2 not found"},
		"a video with no slides": {func(s *studio) { s.slides.Slides = nil }, slideURL, 404, "Slide 2 not found"},
		"slides that are not a list": {
			func(s *studio) { s.slides.Slides = []byte(`{"2": {}}`) }, slideURL, 404, "Slide 2 not found"},
		"the narration is gone from the store": {
			func(s *studio) {
				s.slides.Slides = []byte(`[{"slide_number": 2, "audio_storage_key": "video_presentations/7/31/gone.mp3"}]`)
			}, slideURL, 404, "Slide audio file not found"},
		"the narration is in a backend this tier does not read": {
			func(s *studio) {
				s.slides.Slides = []byte(`[{"slide_number": 2, "audio_storage_key": "` + audioStorageKey + `", "storage_backend": "s3"}]`)
			}, slideURL, 500, "An internal error occurred. Please try again or report this issue if it persists."},
		"a slide number that is not a number": {func(*studio) {}, base + "/slides/two/audio", 422, ""},
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := slideStudio(t)
			test.arrange(s)
			got := s.get(t, test.path, nil)
			if got.status != test.status {
				t.Fatalf("status %d, want %d: %s", got.status, test.status, got.raw)
			}
			if test.message != "" && got.message(t) != test.message {
				t.Errorf("told %q", got.message(t))
			}
			// The path on the rendering machine never reaches a caller.
			if strings.Contains(string(got.raw), "/srv/render") {
				t.Errorf("the answer carries a server path: %s", got.raw)
			}
		})
	}
}

// The ten routes are the owner's and nobody else's. Another account asking
// about an artifact that exists -- every row of it present, a deck and a
// quiz it could write to -- is told what it would be told about an id that
// was never issued: 404, in the same words, with nothing written. There is
// no 403 to tell the two apart by.
//
// However the owner signed in, the routes answer; whether an API key or an
// agent may act for the account at all is decided once, before any route
// (internal/principal).
func TestTheTenRoutesAreTheOwnersAndNobodyElses(t *testing.T) {
	type route struct{ verb, path, body, absent string }
	routes := []route{
		{http.MethodGet, manifestURL, "", artifactAbsent},
		{http.MethodGet, downloadURL, "", artifactAbsent},
		{http.MethodGet, fileURL, "", fileAbsent},
		{http.MethodGet, slideURL, "", artifactAbsent},
		{http.MethodPatch, progressURL, `{"generation": 3, "card_index": 0, "mark": "good"}`, flashcardKind.absent},
		{http.MethodDelete, resetURL, "", flashcardKind.absent},
		{http.MethodPut, orderURL, `{"generation": 3, "order": [0]}`, flashcardKind.absent},
		{http.MethodPut, quizAnswerURL, `{"generation": 3, "question_index": 0, "selected_option_index": 0}`, quizKind.absent},
		{http.MethodPut, quizSkipURL, `{"generation": 3, "question_index": 0}`, quizKind.absent},
		{http.MethodPost, quizRetakeURL, `{"generation": 3, "mode": "all"}`, quizKind.absent},
	}
	// everyRow is an artifact with every row the ten routes read.
	everyRow := func(s *studio, format string) {
		file := &StoredFile{ID: 88, Role: primaryRole, Filename: "deck.json", MimeType: "application/json",
			Backend: "local", Key: deckStorageKey, Checksum: inventedChecksum}
		s.manifest = &ManifestRow{ID: 31, DocumentID: 310, Title: "Somebody's deck", Format: format,
			Generation: 3, Files: []StoredFile{*file}}
		s.download = &Download{Title: "Somebody's deck", Markdown: "private text"}
		s.file = file
		s.slides = &SlideSet{Format: videoFormat, Slides: []byte(`[{"slide_number": 2, "audio_storage_key": "` + audioStorageKey + `"}]`)}
		s.source = &StudySource{Format: format, Generation: 3, File: file}
	}

	t.Run("another account is told the artifact does not exist", func(t *testing.T) {
		for _, r := range routes {
			for _, format := range []string{flashcardsFormat, quizFormat} {
				s := newStudio(t)
				everyRow(s, format)
				s.asker = strangerID
				got := s.call(t, principal.MethodSession, r.verb, r.path, nil, r.body)
				if got.status != http.StatusNotFound {
					t.Errorf("%s %s: status %d, want 404: %s", r.verb, r.path, got.status, got.raw)
					continue
				}
				if got.message(t) != r.absent {
					t.Errorf("%s %s: told %q, want %q", r.verb, r.path, got.message(t), r.absent)
				}
				if strings.Contains(string(got.raw), "private text") || strings.Contains(string(got.raw), "Somebody") {
					t.Errorf("%s %s: the answer carries the owner's content: %s", r.verb, r.path, got.raw)
				}
				if s.writes != 0 || len(s.sections) != 0 {
					t.Errorf("%s %s: wrote to the owner's row", r.verb, r.path)
				}
			}
		}
	})

	// The same words for an id nobody has, and for every way of signing in.
	t.Run("the owner is told the same of an artifact that was never made", func(t *testing.T) {
		for _, method := range []principal.Method{
			principal.MethodSession, principal.MethodPersonalAccessToken, principal.MethodAgent,
		} {
			for _, r := range routes {
				s := newStudio(t)
				got := s.call(t, method, r.verb, r.path, nil, r.body)
				if got.status != http.StatusNotFound || got.message(t) != r.absent {
					t.Errorf("%s %s as %s: status %d: %s", r.verb, r.path, method, got.status, got.raw)
				}
			}
		}
	})
}
