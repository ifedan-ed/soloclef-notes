package artifacts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/corsmirror"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/documentdeletion"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

const (
	origin   = "https://notes.example"
	callerID = "11111111-2222-4333-8444-555555555555"
	// strangerID is a second account, which owns nothing in these tests.
	strangerID = "99999999-2222-4333-8444-555555555555"
)

// fake is the database and the removal for one test. Every title and every
// piece of metadata in these tests is invented.
//
// Everything in it belongs to callerID. Each read is handed the account that
// is asking, as the statements are, and answers "nothing" to any other, so a
// route that forgot to pass the caller -- or passed something the client
// sent -- fails these tests.
type fake struct {
	listed []Listed
	video  *Video
	// document is what the artifact stands on; its Status is rewritten by
	// the two writes, as the column would be.
	document *Document
	// meanwhile runs once before the marker is written: another request
	// getting to the document first.
	meanwhile func(f *fake)
	// removalFails makes the removal stop partway, as a vector store or a
	// file store that is down does.
	removalFails bool
	// removed is the documents the removal was asked for, in order, and
	// cancelled whether any of them was asked for under a context that had
	// already ended.
	removed   []int64
	cancelled bool
}

func (f *fake) ArtifactsIn(_ context.Context, userID string, _ *int64) ([]Listed, error) {
	if userID != callerID {
		return []Listed{}, nil
	}
	return f.listed, nil
}

func (f *fake) VideoIn(_ context.Context, userID string, _ int64) (*Video, error) {
	if userID != callerID {
		return nil, nil
	}
	return f.video, nil
}

func (f *fake) DocumentOf(_ context.Context, userID string, _ int64) (*Document, error) {
	if f.document == nil || userID != callerID {
		return nil, nil
	}
	copied := *f.document
	return &copied, nil
}

func (f *fake) MarkDeleting(_ context.Context, userID string, read Document) (bool, error) {
	if userID != callerID {
		return false, nil
	}
	if f.meanwhile != nil {
		other := f.meanwhile
		f.meanwhile = nil
		other(f)
	}
	if string(f.document.Status) != string(read.Status) {
		return false, nil
	}
	f.document.Status = []byte(`{"state": "deleting"}`)
	return true, nil
}

// Remove is documentdeletion.Removal as the route sees it: it marks the
// document itself (idempotently), and on success the document is gone.
func (f *fake) Remove(ctx context.Context, documentID int64) error {
	f.removed = append(f.removed, documentID)
	f.cancelled = f.cancelled || ctx.Err() != nil
	f.document.Status = []byte(`{"state": "deleting"}`)
	if f.removalFails {
		return errors.New("the vector store is unreachable")
	}
	f.document = nil
	return nil
}

func owner() *fake { return &fake{} }

type answered struct {
	status int
	header http.Header
	raw    []byte
}

func (a answered) message(t *testing.T) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(a.raw, &envelope); err != nil || envelope.Error.RequestID != "req_test" {
		t.Fatalf("not this tier's envelope: %s", a.raw)
	}
	return envelope.Error.Message
}

// ask sends one request to the three routes, mounted behind the request-id
// and CORS middlewares as cmd/api mounts them.
func ask(t *testing.T, f *fake, method principal.Method, verb, path string) answered {
	t.Helper()
	return askAs(t, f, callerID, method, verb, path)
}

// askAs is ask for a named account.
func askAs(t *testing.T, f *fake, userID string, method principal.Method, verb, path string) answered {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	envelope := errorenvelope.Writer{ReportURL: origin + "/contact"}
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "0.0.1"))
	ListRoute{Artifacts: f, Errors: envelope, Logger: logger}.Register(api)
	VideoRoute{Videos: f, Errors: envelope, Logger: logger}.Register(api)
	DeleteRoute{Deletions: f, Removal: f, Errors: envelope, Logger: logger}.Register(api)
	withCaller := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mux.ServeHTTP(response, request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: userID, Method: method})))
	})
	server := httptest.NewServer(requestcontext.Middleware(logger,
		corsmirror.NewPolicy([]string{origin}).Middleware(withCaller)))
	defer server.Close()

	request, err := http.NewRequest(verb, server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Request-ID", "req_test")
	request.Header.Set("Origin", origin)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return answered{status: response.StatusCode, header: response.Header, raw: raw}
}

const (
	listPath   = "/api/v1/artifacts"
	videoPath  = "/api/v1/artifacts/31/video"
	deletePath = "/api/v1/artifacts/31"
)

var (
	made    = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	changed = time.Date(2026, 3, 4, 5, 6, 8, 250_000_000, time.UTC)
)

func pointer[T any](value T) *T { return &value }

func TestTheListIsEveryArtifactInTheShapeTheStudioReads(t *testing.T) {
	f := owner()
	f.listed = []Listed{
		{ID: 33, DocumentID: 330, Title: "Field notes — “quoted”", Format: "markdown", Generation: 1,
			Status: []byte(`{"state": "processing"}`), CreatedAt: made, UpdatedAt: &changed},
		{ID: 32, DocumentID: 320, Title: "A deck", Format: "flashcards", Generation: 4,
			ThreadID: pointer(int64(12)), CreatedAt: made, UpdatedAt: &changed,
			Metadata: []byte(`{"legacy": {"kind": "podcast", "id": 7, "extra": "dropped"}}`)},
	}
	got := ask(t, f, principal.MethodSession, http.MethodGet, listPath)
	const want = `[` +
		`{"artifact_id":33,"document_id":330,"title":"Field notes — “quoted”","format":"markdown","generation":1,` +
		`"indexing_status":"processing","thread_id":null,"created_at":"2026-03-04T05:06:07+00:00",` +
		`"updated_at":"2026-03-04T05:06:08.250000+00:00"},` +
		`{"artifact_id":32,"document_id":320,"title":"A deck","format":"flashcards","generation":4,` +
		`"indexing_status":"ready","thread_id":12,"created_at":"2026-03-04T05:06:07+00:00",` +
		`"updated_at":"2026-03-04T05:06:08.250000+00:00","legacy":{"kind":"podcast","id":7}}]`
	if got.status != http.StatusOK || string(got.raw) != want {
		t.Errorf("status %d\n got  %s\n want %s", got.status, got.raw, want)
	}
	if got.header.Get("Cache-Control") != "private, no-store" {
		t.Errorf("Cache-Control %q", got.header.Get("Cache-Control"))
	}
}

// One artifact with a column nobody expected must not cost the owner their
// whole list.
func TestARowThatCannotBeReadIsStillListed(t *testing.T) {
	for name, row := range map[string]Listed{
		"a status that is a list":       {Status: []byte(`["ready"]`)},
		"a status that is text":         {Status: []byte(`"ready"`)},
		"a state that is null":          {Status: []byte(`{"state": null}`)},
		"a state that is a number":      {Status: []byte(`{"state": 1.5}`)},
		"metadata that is a list":       {Metadata: []byte(`[{"legacy": {}}]`)},
		"metadata that is true":         {Metadata: []byte(`true`)},
		"a legacy id that is true":      {Metadata: []byte(`{"legacy": {"kind": "image", "id": true}}`)},
		"a legacy id that is a decimal": {Metadata: []byte(`{"legacy": {"kind": "image", "id": 7.0}}`)},
		"a legacy kind that is missing": {Metadata: []byte(`{"legacy": {"id": 7}}`)},
		// The web app's schema wants a positive id and refuses the whole
		// list over one that is not.
		"a legacy id that is zero":     {Metadata: []byte(`{"legacy": {"kind": "image", "id": 0}}`)},
		"a legacy id that is negative": {Metadata: []byte(`{"legacy": {"kind": "image", "id": -4}}`)},
	} {
		t.Run(name, func(t *testing.T) {
			f := owner()
			row.ID, row.Title, row.Format, row.CreatedAt = 31, "Odd one", "pdf", made
			f.listed = []Listed{row, {ID: 30, Title: "Fine", Format: "pdf", CreatedAt: made}}
			got := ask(t, f, principal.MethodSession, http.MethodGet, listPath)
			var items []map[string]any
			if err := json.Unmarshal(got.raw, &items); got.status != http.StatusOK || err != nil || len(items) != 2 {
				t.Fatalf("status %d: %s", got.status, got.raw)
			}
			// The state is always a word, and a reference that is not one
			// is left out rather than sent half-formed.
			if items[0]["indexing_status"] != "ready" {
				t.Errorf("indexing_status is %v", items[0]["indexing_status"])
			}
			if _, present := items[0]["legacy"]; present {
				t.Errorf("a legacy reference was made of %s", row.Metadata)
			}
		})
	}
}

func TestTheVideoPayload(t *testing.T) {
	f := owner()
	f.video = &Video{ID: 31, Title: "A short film", Format: "video", ThreadID: pointer(int64(5)),
		Metadata: []byte(`{"slides": [` +
			`"not a slide", ` +
			`{"slide_number": 1, "title": "One", "audio_storage_key": "media/one.mp3", "audio_file": "/srv/media/one.mp3", "storage_backend": "local"}, ` +
			`{"slide_number": 2, "title": "Two", "audio_file": "/tmp/two.wav"}, ` +
			`{"slide_number": 3, "title": "Silent"}, ` +
			`{"slide_number": true, "audio_storage_key": "media/x.mp3"}, ` +
			`{"title": "Unnumbered", "audio_storage_key": "media/y.mp3", "audio_url": "https://elsewhere.example/y"}], ` +
			`"scene_codes": ["scene one", {"code": "two"}]}`)}
	got := ask(t, f, principal.MethodSession, http.MethodGet, videoPath)
	// No storage key, no server path and no backend name on any slide; a URL
	// only where there is narration *and* a number to fetch it by; and a
	// count of the slides actually sent.
	const want = `{"artifact_id":31,"title":"A short film","status":"ready","slides":[` +
		`{"slide_number":1,"title":"One","audio_url":"/api/v1/artifacts/31/slides/1/audio"},` +
		`{"slide_number":2,"title":"Two","audio_url":"/api/v1/artifacts/31/slides/2/audio"},` +
		`{"slide_number":3,"title":"Silent","audio_url":null},` +
		`{"slide_number":true,"audio_url":null},` +
		`{"title":"Unnumbered","audio_url":null}],` +
		`"scene_codes":["scene one",{"code":"two"}],"slide_count":5,"thread_id":5}`
	if got.status != http.StatusOK || string(got.raw) != want {
		t.Errorf("status %d\n got  %s\n want %s", got.status, got.raw, want)
	}
}

func TestAVideoWithNothingToPlayIsNotFound(t *testing.T) {
	for name, c := range map[string]struct {
		video   *Video
		message string
	}{
		"no such artifact":          {nil, artifactAbsent},
		"not a video":               {&Video{Format: "pdf"}, notAVideo},
		"no metadata":               {&Video{Format: "video"}, payloadAbsent},
		"no scene codes":            {&Video{Format: "video", Metadata: []byte(`{"slides": []}`)}, payloadAbsent},
		"slides that are an object": {&Video{Format: "video", Metadata: []byte(`{"slides": {}, "scene_codes": []}`)}, payloadAbsent},
		"metadata that is a list":   {&Video{Format: "video", Metadata: []byte(`[{"slides": [], "scene_codes": []}]`)}, payloadAbsent},
		"metadata that is a truth":  {&Video{Format: "video", Metadata: []byte(`true`)}, payloadAbsent},
	} {
		t.Run(name, func(t *testing.T) {
			f := owner()
			f.video = c.video
			got := ask(t, f, principal.MethodSession, http.MethodGet, videoPath)
			if got.status != http.StatusNotFound || got.message(t) != c.message {
				t.Errorf("status %d: %s", got.status, got.raw)
			}
		})
	}
}

// An artifact is its owner's and nobody else's. However the owner signed in
// -- a browser session, an API key, an agent's token -- the three routes
// answer; whether keys may act for the account at all is the sign-in gate's
// question, asked once before any route (internal/principal).
//
// Another account asking about the same artifact is told exactly what it
// would be told about an id that was never issued, and changes nothing.
func TestAnArtifactIsItsOwnersAndNobodyElses(t *testing.T) {
	stocked := func() *fake {
		return &fake{
			listed:   []Listed{{ID: 31, DocumentID: 310, Title: "A film", Format: "video", CreatedAt: made, UpdatedAt: &changed}},
			video:    &Video{ID: 31, Format: "video", Metadata: []byte(`{"slides": [], "scene_codes": []}`)},
			document: &Document{ID: 310, Status: []byte(`{"state": "ready"}`)},
		}
	}
	for _, route := range []struct {
		name, verb, path string
		allowed          int
		// stranger is the status another account gets, and strangerBody its
		// body when that is not the 404 envelope.
		stranger     int
		strangerBody string
	}{
		{"list", http.MethodGet, listPath, 200, 200, `[]`},
		{"video", http.MethodGet, videoPath, 200, 404, ""},
		{"delete", http.MethodDelete, deletePath, 204, 404, ""},
	} {
		for _, method := range []principal.Method{
			principal.MethodSession, principal.MethodPersonalAccessToken, principal.MethodAgent,
		} {
			t.Run(route.name+"/the owner by "+string(method), func(t *testing.T) {
				got := ask(t, stocked(), method, route.verb, route.path)
				if got.status != route.allowed {
					t.Fatalf("status %d, want %d: %s", got.status, route.allowed, got.raw)
				}
				if got.header.Get("Access-Control-Allow-Origin") != origin {
					t.Error("the answer carries no CORS header")
				}
			})
		}
		t.Run(route.name+"/another account", func(t *testing.T) {
			f := stocked()
			got := askAs(t, f, strangerID, principal.MethodSession, route.verb, route.path)
			if got.status != route.stranger {
				t.Fatalf("status %d, want %d: %s", got.status, route.stranger, got.raw)
			}
			if route.stranger == 404 {
				// The same words as an artifact that was never made.
				if got.message(t) != artifactAbsent {
					t.Errorf("another account was told %q", got.message(t))
				}
			} else if string(got.raw) != route.strangerBody {
				t.Errorf("another account was shown %s", got.raw)
			}
			if len(f.removed) != 0 || f.document == nil || string(f.document.Status) != `{"state": "ready"}` {
				t.Error("another account's request changed the owner's artifact")
			}
		})
	}
	// A bad id is the 422, whoever asks.
	got := ask(t, &fake{}, principal.MethodSession, http.MethodDelete, "/api/v1/artifacts/abc")
	if got.status != http.StatusUnprocessableEntity {
		t.Errorf("status %d: %s", got.status, got.raw)
	}
}

func TestADeleteRemovesTheArtifactsDocumentBeforeItAnswers(t *testing.T) {
	for name, status := range map[string]string{
		"ready": `{"state": "ready"}`, "failed, with its reason": `{"state": "failed", "reason": "no text"}`,
		"a status naming no state": `{}`, "no status at all": ``, "a status that is not an object": `[1]`,
		// An earlier delete that stopped partway: finished, not refused.
		"already marked deleting": `{"state": "deleting"}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := owner()
			f.document = &Document{ID: 310, Status: []byte(status)}
			got := ask(t, f, principal.MethodSession, http.MethodDelete, deletePath)
			if got.status != http.StatusNoContent || len(got.raw) != 0 {
				t.Fatalf("status %d: %s", got.status, got.raw)
			}
			if got.header.Get("Cache-Control") != "private, no-store" {
				t.Errorf("Cache-Control %q", got.header.Get("Cache-Control"))
			}
			// The removal ran, once, for the artifact's document, and the
			// document is gone by the time the 204 is written.
			if len(f.removed) != 1 || f.removed[0] != 310 || f.document != nil {
				t.Errorf("removed %v, document left %+v", f.removed, f.document)
			}
			if f.cancelled {
				t.Error("the removal ran under a context that had ended")
			}
		})
	}
}

// A document the generation job or the indexer is working on is not deleted
// underneath it.
func TestADeleteRefusesADocumentSomethingIsWorkingOn(t *testing.T) {
	for _, state := range []string{"pending", "processing"} {
		t.Run(state, func(t *testing.T) {
			f := owner()
			stored := `{"state": "` + state + `"}`
			f.document = &Document{ID: 310, Status: []byte(stored)}
			got := ask(t, f, principal.MethodSession, http.MethodDelete, deletePath)
			if got.status != http.StatusConflict || got.message(t) != beingGenerated {
				t.Fatalf("status %d: %s", got.status, got.raw)
			}
			if string(f.document.Status) != stored || len(f.removed) != 0 {
				t.Errorf("a refused delete changed something: %s, %v", f.document.Status, f.removed)
			}
		})
	}
}

func TestADeleteOfNothingIsNotFound(t *testing.T) {
	got := ask(t, owner(), principal.MethodSession, http.MethodDelete, deletePath)
	if got.status != http.StatusNotFound || got.message(t) != artifactAbsent {
		t.Errorf("status %d: %s", got.status, got.raw)
	}
}

// An indexer or a generation job starting between the read and the claim:
// the document is not removed underneath it.
func TestADeleteThatLosesARaceRemovesNothing(t *testing.T) {
	f := owner()
	f.document = &Document{ID: 310, Status: []byte(`{"state": "ready"}`)}
	f.meanwhile = func(other *fake) { other.document.Status = []byte(`{"state": "processing", "stage": "parsing"}`) }
	got := ask(t, f, principal.MethodSession, http.MethodDelete, deletePath)
	if got.status != http.StatusConflict || got.message(t) != changedMeanwhile || len(f.removed) != 0 {
		t.Errorf("status %d, removed %v: %s", got.status, f.removed, got.raw)
	}
}

// A store that is down partway through: the caller is told to delete again,
// the document stays marked (out of search), and the second delete finishes.
func TestARemovalThatStopsPartwayIsFinishedByDeletingAgain(t *testing.T) {
	f := owner()
	f.removalFails = true
	f.document = &Document{ID: 310, Status: []byte(`{"state": "failed", "reason": "the page answered 503"}`)}
	got := ask(t, f, principal.MethodSession, http.MethodDelete, deletePath)
	if got.status != http.StatusServiceUnavailable || got.message(t) != documentdeletion.Unfinished {
		t.Fatalf("status %d: %s", got.status, got.raw)
	}
	if f.document == nil || string(f.document.Status) != `{"state": "deleting"}` {
		t.Fatalf("the document is left %+v, and should be left marked", f.document)
	}

	f.removalFails = false
	got = ask(t, f, principal.MethodSession, http.MethodDelete, deletePath)
	if got.status != http.StatusNoContent || f.document != nil || len(f.removed) != 2 {
		t.Errorf("the second delete: status %d, removed %v, document %+v", got.status, f.removed, f.document)
	}
}
