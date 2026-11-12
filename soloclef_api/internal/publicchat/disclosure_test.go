package publicchat

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// These tests pin what the three routes send to someone who holds nothing
// but a link. Every one of them states a fact about the *Python* routes,
// mirrored here: several are things a reader would call a leak, and none of
// them is fixed in this package, because a route is identical or it has not
// moved. They exist so that changing what a public route discloses -- in
// either direction -- is changing a test, and cannot happen in passing.
//
// The answers themselves are all in the recorded table; what these add is
// the sentence saying what each one means.

// page renders the page of a chat snapshot holding one message.
func pageOf(t *testing.T, data string) string {
	t.Helper()
	decoded, err := pythonjson.Decode([]byte(data))
	if err != nil {
		t.Fatalf("decoding: %v", err)
	}
	page, err := BuildPage(context.Background(), &world{}, "tok", Snapshot{}, decoded.(*pythonjson.Object))
	if err != nil {
		t.Fatalf("building the page: %v", err)
	}
	body, err := pythonjson.Marshal(page)
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	return string(body)
}

func video(t *testing.T, metadata string) string {
	t.Helper()
	title := "A film"
	payload, absent, err := BuildVideo("tok", VideoArtifact{ID: 31, Title: &title, Metadata: []byte(metadata)})
	if err != nil || absent {
		t.Fatalf("building the payload: absent=%v err=%v", absent, err)
	}
	body, err := renderUnmodelled(payload)
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	return string(body)
}

// A slide that has been moved into storage has its storage key removed and
// its old server path removed -- unless it has *both*, in which case the
// path survives. `slide.pop("audio_storage_key") or slide.pop("audio_file")`
// stops at the first truthy value, so the second pop never runs.
//
// The writer of this metadata removes `audio_file` when it sets the key
// (app/artifacts/media/video/storage.py), so a slide holding both is a row
// from before that, or one an interrupted upload left. For such a row, the
// path of a file on the server's disk is sent to whoever holds the link.
func TestAVideoSlideKeepsItsServerPathWhenItAlsoHasAStorageKey(t *testing.T) {
	both := video(t, `{"slides": [{"slide_number": 1, "audio_storage_key": "media/1.mp3", "audio_file": "/srv/media/1.mp3"}], "scene_codes": []}`)
	if !strings.Contains(both, `"audio_file":"/srv/media/1.mp3"`) {
		t.Errorf("a slide with a storage key and a server path no longer sends the path: %s", both)
	}
	if strings.Contains(both, "audio_storage_key") {
		t.Errorf("the storage key itself is never sent: %s", both)
	}
	// With the path alone, or with a key that is empty, the path is removed.
	for _, metadata := range []string{
		`{"slides": [{"slide_number": 1, "audio_file": "/srv/media/1.mp3"}], "scene_codes": []}`,
		`{"slides": [{"slide_number": 1, "audio_storage_key": "", "audio_file": "/srv/media/1.mp3"}], "scene_codes": []}`,
	} {
		if alone := video(t, metadata); strings.Contains(alone, "/srv/media") {
			t.Errorf("a server path on its own is sent: %s", alone)
		}
	}
}

// The video payload is not passed through app/public_sharing/redaction.py at
// all. Three keys are taken out of each slide by name and nothing else is:
// any other key of a slide, and every key of every scene code, is sent as
// stored -- including the ones the page route strips from a message.
func TestAVideoPayloadIsSentUnredacted(t *testing.T) {
	sent := video(t, `{"slides": [{"slide_number": 1, "storage_key": "s/1", "file_path": "/srv/1", "email": "someone@example.com", "token": "t"}],`+
		` "scene_codes": [{"code": "c", "storage_key": "scenes/1.tsx", "api_key": "k"}], "notes": "not a slide"}`)
	for _, kept := range []string{
		`"storage_key":"s/1"`, `"file_path":"/srv/1"`, `"email":"someone@example.com"`, `"token":"t"`,
		`"storage_key":"scenes/1.tsx"`, `"api_key":"k"`,
	} {
		if !strings.Contains(sent, kept) {
			t.Errorf("the video payload no longer sends %s: %s", kept, sent)
		}
	}
	// Only `slides` and `scene_codes` are read off the metadata; a sibling
	// key is not sent.
	if strings.Contains(sent, "not a slide") {
		t.Errorf("a key of the metadata beside slides and scene_codes is sent: %s", sent)
	}
}

// A shared video is served as the artifact is *now*. The snapshot holds the
// artifact's id and nothing of its content, so a video regenerated or edited
// after the link was made is what every existing link shows -- unlike the
// chat's messages and the podcast's transcript, which were copied.
func TestASharedVideoIsReadOffTheArtifactAsItIsNow(t *testing.T) {
	state := &world{now: time.Unix(1_700_000_000, 0)}
	server := httptest.NewServer(served(state, "https://notes.example"))
	defer server.Close()

	answers := map[string]bool{}
	for _, metadata := range []string{
		`{"slides": [{"slide_number": 1, "title": "As shared"}], "scene_codes": []}`,
		`{"slides": [{"slide_number": 1, "title": "As edited afterwards"}], "scene_codes": []}`,
	} {
		// One snapshot, unchanged between the two requests.
		var c recordedCase
		c.Snapshot = &struct {
			OutputID *int64 `json:"notebook_output_id"`
			Expires  string `json:"expires"`
			Data     string `json:"data"`
		}{Expires: "none", Data: `{"artifact_ids": [31]}`}
		stored := metadata
		c.Artifacts = append(c.Artifacts, struct {
			ID         int64   `json:"id"`
			Format     string  `json:"format"`
			Generation int64   `json:"generation"`
			Title      *string `json:"title"`
			Metadata   *string `json:"metadata"`
		}{ID: 31, Format: "video", Generation: 1, Metadata: &stored})
		state.current = c
		response, err := http.Get(server.URL + "/api/v1/public/tok/artifacts/31/video")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		answers[string(body)] = true
	}
	if len(answers) != 2 {
		t.Errorf("the same link answered the same payload for two states of its artifact: %v", answers)
	}
}

// The podcast answer has four keys and no fifth. A snapshot from before audio
// moved into artifact storage carries the file's storage key and its path on
// the server; a current one carries the artifact's id.
// None is sent. What is sent is sent whole, though: `title` and `transcript`
// are not redacted, so an internal key inside them is kept.
func TestAPodcastSendsFourKeysAndNothingElseOfItsEntry(t *testing.T) {
	entry, err := pythonjson.Decode([]byte(`{"original_id": 3, "title": "Episode", ` +
		`"transcript": [{"dialog": "Hello.", "file_path": "/srv/kept"}], "storage_key": "podcasts/3.mp3", ` +
		`"file_path": "/srv/podcasts/3.mp3", "artifact_id": 31}`))
	if err != nil {
		t.Fatal(err)
	}
	answer := BuildPodcast(entry.(*pythonjson.Object))
	if got, want := answer.Keys(), []string{"id", "title", "status", "podcast_transcript"}; !reflect.DeepEqual(got, want) {
		t.Errorf("the podcast answer has the keys %v, want %v", got, want)
	}
	body, err := renderUnmodelled(answer)
	if err != nil {
		t.Fatal(err)
	}
	for _, unsent := range []string{"podcasts/3.mp3", "/srv/podcasts/3.mp3", "artifact_id"} {
		if strings.Contains(string(body), unsent) {
			t.Errorf("the podcast answer sends %q: %s", unsent, body)
		}
	}
	if !strings.Contains(string(body), `"file_path":"/srv/kept"`) {
		t.Errorf("a key inside the transcript is now removed, which the Python route does not do: %s", body)
	}
}

// The page strips twelve keys from its messages, by exact name. This is the
// list, and a thirteenth is a change to what a stranger is sent.
func TestThePageStripsExactlyTheseTwelveKeys(t *testing.T) {
	stripped := make([]string, 0, len(internalKeys))
	for key := range internalKeys {
		stripped = append(stripped, key)
	}
	sort.Strings(stripped)
	want := []string{
		"api_key", "audio_file", "audio_storage_key", "author_id", "created_by_id", "email",
		"file_path", "storage_backend", "storage_key", "token", "user_email", "user_id",
	}
	if !reflect.DeepEqual(stripped, want) {
		t.Errorf("the page strips %v, want %v", stripped, want)
	}
}

// The match is exact and case-sensitive, so a key that *means* the same as a
// stripped one and is spelled differently is sent: "token" goes and
// "access_token", "Token", "password", "secret" and "authorization" stay.
// The values at risk are the arguments and results of the tool calls a
// snapshot keeps (image, podcast, video and saved-artifact cards).
func TestThePageKeepsAKeyThatIsNotSpelledExactlyAsAStrippedOne(t *testing.T) {
	page := pageOf(t, `{"snapshot_at": "2026-03-04T05:06:07+00:00", "messages": [{"role": "assistant", "created_at": "2026-03-04T05:06:07+00:00",`+
		` "content": [{"type": "tool-call", "toolName": "generate_image", "args": {"token": "gone", "access_token": "kept-1", "Token": "kept-2"},`+
		` "result": {"email": "gone@example.com", "password": "kept-3", "secret": "kept-4", "authorization": "kept-5", "Email": "kept@example.com"}}]}]}`)
	for _, gone := range []string{`"gone"`, "gone@example.com"} {
		if strings.Contains(page, gone) {
			t.Errorf("a stripped key's value is sent (%s): %s", gone, page)
		}
	}
	for _, kept := range []string{"kept-1", "kept-2", "kept-3", "kept-4", "kept-5", "kept@example.com"} {
		if !strings.Contains(page, kept) {
			t.Errorf("%s is no longer sent, which is a different page from the Python tier's: %s", kept, page)
		}
	}
}

// A message's author is a display name and a null avatar, whatever the
// snapshot stored: older snapshots stored the account's id and its avatar
// URL. The display name is sent as stored, so an account whose display name
// *is* its address is named by that address. (The route that writes
// snapshots falls back to the word "User", not to the address, for an
// account with no display name -- unlike the invite info route.)
func TestAnAuthorIsADisplayNameAndNothingElse(t *testing.T) {
	page := pageOf(t, `{"snapshot_at": "2026-03-04T05:06:07+00:00", "messages": [`+
		`{"role": "user", "content": null, "created_at": "2026-03-04T05:06:07+00:00",`+
		` "author": {"id": "11111111-2222-4333-8444-555555555555", "display_name": "Ada Example", "avatar_url": "https://elsewhere.example/a.png", "email": "ada@example.com"}},`+
		`{"role": "user", "content": null, "created_at": "2026-03-04T05:06:07+00:00", "author": {"display_name": "someone@example.com"}}]}`)
	for _, unsent := range []string{"11111111-2222-4333-8444-555555555555", "elsewhere.example", "ada@example.com"} {
		if strings.Contains(page, unsent) {
			t.Errorf("the page sends %q of an author: %s", unsent, page)
		}
	}
	for _, sent := range []string{
		`"author":{"display_name":"Ada Example","avatar_url":null}`,
		`"author":{"display_name":"someone@example.com","avatar_url":null}`,
	} {
		if !strings.Contains(page, sent) {
			t.Errorf("the page does not send %s: %s", sent, page)
		}
	}
}

// What a chat's page does *not* carry of its snapshot: a message's id, the
// snapshot's own author, its podcasts and its artifact allowlist. What it
// does carry, on purpose, is a tool result whole: the image card reads it,
// and an id inside it grants nothing (redaction.py says so).
func TestAPageSendsNoIdsButAToolResultWhole(t *testing.T) {
	page := pageOf(t, `{"title": "T", "snapshot_at": "2026-03-04T05:06:07+00:00", "author": {"display_name": "The sharer"},`+
		` "podcasts": [{"original_id": 3, "title": "An unsent episode"}], "artifact_ids": [4242],`+
		` "messages": [{"id": 987654, "thread_id": 1234567, "role": "assistant", "created_at": "2026-03-04T05:06:07+00:00",`+
		` "content": [{"type": "tool-call", "toolName": "generate_image", "result": {"image_id": 7}}]}]}`)
	for _, unsent := range []string{"987654", "1234567", "The sharer", "An unsent episode", "4242"} {
		if strings.Contains(page, unsent) {
			t.Errorf("the page sends %q: %s", unsent, page)
		}
	}
	if !strings.Contains(page, `"image_id":7`) {
		t.Errorf("the page no longer sends a tool result whole: %s", page)
	}
}

// A shared output lists its artifact's files with six fields each and no
// seventh. Where the bytes are -- backend, key, checksum -- is not even read
// (queries.sql), so it cannot be sent.
func TestAnOutputsFilesCarrySixFieldsAndNoStorageDetail(t *testing.T) {
	state := &world{}
	state.current.Artifacts = append(state.current.Artifacts, struct {
		ID         int64   `json:"id"`
		Format     string  `json:"format"`
		Generation int64   `json:"generation"`
		Title      *string `json:"title"`
		Metadata   *string `json:"metadata"`
	}{ID: 31, Format: "slides", Generation: 2})
	state.current.Files = append(state.current.Files, struct {
		ID         int64  `json:"id"`
		ArtifactID int64  `json:"artifact_id"`
		Role       string `json:"role"`
		Filename   string `json:"filename"`
		MimeType   string `json:"mime_type"`
		Size       int64  `json:"size_bytes"`
	}{ID: 8, ArtifactID: 31, Role: "primary", Filename: "deck.pptx", MimeType: "application/pdf", Size: 10})
	_, files, err := sharedOutputFiles(context.Background(), state, "tok", 31)
	if err != nil || len(files) != 1 {
		t.Fatalf("listing the files: %v (%d)", err, len(files))
	}
	want := []string{"file_id", "role", "filename", "mime_type", "size_bytes", "content_url"}
	if got := files[0].(*pythonjson.Object).Keys(); !reflect.DeepEqual(got, want) {
		t.Errorf("a listed file has the keys %v, want %v", got, want)
	}
	if fields := reflect.TypeOf(StoredFile{}).NumField(); fields != 5 {
		t.Errorf("StoredFile has %d fields; a sixth is a column a public route can now see", fields)
	}
	if fields := reflect.TypeOf(Snapshot{}).NumField(); fields != 3 {
		t.Errorf("Snapshot has %d fields; a fourth is a column a public route can now see", fields)
	}
}

// Only the page tells an expired link from one that never existed: 410
// against 404. The other two routes answer one 404 for both, so the page is
// the one place a stranger can learn that a token *was* a link. That is
// deliberate -- the web page says "this link has expired" -- and it is the
// only thing an expired link still says about itself.
func TestOnlyThePageTellsAnExpiredLinkFromAMissingOne(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	past := now.Add(-time.Hour)
	state := &world{now: now, expiries: map[string]*time.Time{"past": &past}}
	server := httptest.NewServer(served(state, "https://notes.example"))
	defer server.Close()

	status := func(path string, expired bool) int {
		state.current = recordedCase{}
		if expired {
			state.current.Snapshot = &struct {
				OutputID *int64 `json:"notebook_output_id"`
				Expires  string `json:"expires"`
				Data     string `json:"data"`
			}{Expires: "past", Data: `{"title": "T", "podcasts": [{"original_id": 3}], "artifact_ids": [31]}`}
		}
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response.StatusCode
	}
	for path, want := range map[string][2]int{
		"/api/v1/public/tok":                    {http.StatusGone, http.StatusNotFound},
		"/api/v1/public/tok/artifacts/31/video": {http.StatusNotFound, http.StatusNotFound},
		"/api/v1/public/tok/podcasts/3":         {http.StatusNotFound, http.StatusNotFound},
	} {
		if got := [2]int{status(path, true), status(path, false)}; got != want {
			t.Errorf("%s: expired and missing answered %v, want %v", path, got, want)
		}
	}
}

// These routes never look at a credential. One that is malformed is not a
// 401, and one that is valid changes nothing: the answer is the answer for
// the token in the path. The routes are served here with no principal in the
// request's context at all, which is how cmd/api mounts them, so a route
// that began to ask for one would fail every test in this package.
func TestACredentialIsNeverLookedAt(t *testing.T) {
	state := &world{now: time.Unix(1_700_000_000, 0)}
	server := httptest.NewServer(served(state, "https://notes.example"))
	defer server.Close()

	answers := map[string]bool{}
	for _, headers := range []map[string]string{
		{},
		{"Authorization": "Bearer not-a-token"},
		{"Authorization": "Basic !!!", "Cookie": "soloclef_session=garbage"},
	} {
		state.current = recordedCase{}
		state.current.Snapshot = &struct {
			OutputID *int64 `json:"notebook_output_id"`
			Expires  string `json:"expires"`
			Data     string `json:"data"`
		}{Expires: "none", Data: `{"title": "T", "podcasts": [{"original_id": 3, "title": "Episode"}]}`}
		request, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/public/tok/podcasts/3", nil)
		for name, value := range headers {
			request.Header.Set(name, value)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Errorf("with %v the route answered %d: %s", headers, response.StatusCode, body)
		}
		answers[string(body)] = true
	}
	if len(answers) != 1 {
		t.Errorf("the answer depends on the credential sent: %v", answers)
	}
}

// The share token is written into two kinds of URL and escaped in only one.
// A file's `content_url` percent-encodes it; a slide's `audio_url` is an
// f-string and carries it exactly as the path held it. A real token is 64
// URL-safe characters, so the two agree for every link this app makes.
func TestTheTokenIsQuotedInAFileURLAndNotInAnAudioURL(t *testing.T) {
	if got, want := quoted(`a b<c>"é/~._-`), "a%20b%3Cc%3E%22%C3%A9%2F~._-"; got != want {
		t.Errorf("quoted: got %s, want %s", got, want)
	}
	payload, _, err := BuildVideo(`a b<c>`, VideoArtifact{ID: 31, Metadata: []byte(
		`{"slides": [{"slide_number": 1, "audio_storage_key": "k"}], "scene_codes": []}`)})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := renderUnmodelled(payload)
	if want := `"audio_url":"/api/v1/public/a b<c>/artifacts/31/slides/1/audio"`; !strings.Contains(string(body), want) {
		t.Errorf("the audio URL is not %s: %s", want, body)
	}
}
