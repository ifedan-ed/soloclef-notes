package nextcloudimport

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice/parserchoicetest"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/uploadvision"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/nextcloud"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

// desk is the three routes over a stand-in database and a stand-in
// Nextcloud, for one test.
type desk struct {
	t       *testing.T
	api     huma.API
	table   *table
	cloud   *cloud
	handed  *handed
	as      principal.Principal
	events  journal
	connect int // how many times a client was made
	// vision is the refusal of use_vision_llm the caller gets, "" for a
	// caller who has a vision model.
	vision *visionGate
}

// visionGate is the vision check (uploadvision.Vision) of one test.
type visionGate struct{ refusal string }

func (g *visionGate) Refusal(context.Context, string) (string, error) { return g.refusal, nil }

const (
	ownCloud    = int64(12) // the caller's connection to the stand-in Nextcloud
	othersCloud = int64(13) // somebody else's
	elsewhere   = int64(14) // the caller's, made for another server
	notebook    = int64(40) // the caller's notebook
	othersBook  = int64(41) // somebody else's
)

func newDesk(t *testing.T, files map[string]node) *desk {
	t.Helper()
	d := &desk{t: t, cloud: newCloud(t, files), as: principal.Principal{UserID: owner, Method: principal.MethodSession},
		vision: &visionGate{refusal: uploadvision.NoModel}}
	config := `{"server_url": "` + d.cloud.server.URL + `", "username": "ada", "login_name": "ada@example.org", "app_password": "app-password"}`
	d.table = &table{
		nextID: 100, documents: map[int64]*held{}, notebooks: map[int64]string{notebook: owner, othersBook: somebody}, events: &d.events,
		connections: map[int64]struct{ owner, config string }{
			ownCloud:    {owner, config},
			othersCloud: {somebody, config},
			elsewhere:   {owner, `{"server_url": "https://another.example", "username": "ada", "app_password": "x"}`},
		},
	}
	d.handed = &handed{events: &d.events}
	credentials, err := nextcloud.NewCredentials("a-secret", d.cloud.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	d.api = humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	Routes{
		Rows: d.table, Credentials: credentials, Downloads: d.handed, MaxFileBytes: 100, Vision: d.vision,
		// Local is the default; Datalab is configured; the other two are not.
		Parsers: parserchoicetest.Registry(t, parse.Local, parse.Datalab),
		Connect: func(account Account) nextcloud.Remote {
			d.connect++
			return nextcloud.NewClient(account, d.cloud.server.Client().Transport, quick)
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}.Register(d.api)
	return d
}

// answer is what a route said.
type answer struct {
	status int
	body   string
	// code and message are the error envelope's, for anything but a 200.
	code, message string
}

func (d *desk) post(path, body string) answer {
	d.t.Helper()
	return d.ask(http.MethodPost, path, body)
}

func (d *desk) ask(method, path, body string) answer {
	d.t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request = request.WithContext(principal.WithPrincipal(request.Context(), d.as))
	recorder := httptest.NewRecorder()
	d.api.Adapter().ServeHTTP(recorder, request)
	got := answer{status: recorder.Code, body: recorder.Body.String()}
	if recorder.Code != http.StatusOK {
		var envelope struct {
			Error struct {
				Code, Message string
				Status        int
			}
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil || envelope.Error.Status != recorder.Code {
			d.t.Fatalf("a refusal that is not the error envelope: %s", recorder.Body)
		}
		got.code, got.message = envelope.Error.Code, envelope.Error.Message
	}
	return got
}

func plans() map[string]node {
	return map[string]node{
		"/Plans":           {folder: true},
		"/Plans/a.pdf":     {content: "aaaa", etag: "ea"},
		"/Plans/photo.png": {content: "png"},
		"/Plans/huge.pdf":  {size: 500},
		"/loose.pdf":       {content: "loose", etag: "el"},
	}
}

const (
	previewOwn = "/api/v1/connectors/12/nextcloud/import/preview"
	importOwn  = "/api/v1/connectors/12/nextcloud/import"
	pickPlans  = `{"items": [{"path": "/Plans", "kind": "folder"}, {"path": "/loose.pdf", "kind": "file"}], "use_vision_llm": false, "notebook_id": null}`
)

func TestAPreviewSaysWhatAnImportWouldBringIn(t *testing.T) {
	d := newDesk(t, plans())
	got := d.post(previewOwn, pickPlans)
	if got.status != http.StatusOK {
		t.Fatalf("answered %d %s", got.status, got.body)
	}
	if want := `{"file_count":2,"total_bytes":9,"unsupported_count":1,"too_large_count":1,"over_limit":false,"max_files":200}`; got.body != want {
		t.Errorf("answered %s\nwant     %s", got.body, want)
	}
	// A preview writes nothing and starts nothing.
	if len(d.table.starts) != 0 || len(d.handed.jobs) != 0 {
		t.Error("a preview started an import")
	}
}

func TestAnImportRecordsTheDocumentsThenHandsOverTheDownloads(t *testing.T) {
	d := newDesk(t, plans())
	got := d.post(importOwn, `{"items": [{"path": "/Plans", "kind": "folder"}, {"path": "/loose.pdf", "kind": "file"}],
		"parser": "datalab", "use_vision_llm": false, "notebook_id": 40}`)
	if got.status != http.StatusOK {
		t.Fatalf("answered %d %s", got.status, got.body)
	}
	// Under Datalab the .png is read too; the oversized file is still out.
	if want := `{"document_ids":[102,103,104],"folder_ids":[101],"queued":3,"reused":0,"unsupported":[],"too_large":["/Plans/huge.pdf"]}`; got.body != want {
		t.Errorf("answered %s\nwant     %s", got.body, want)
	}

	// The transaction was given the parser by name and the notebook.
	if len(d.table.starts) != 1 {
		t.Fatalf("%d imports reached the database", len(d.table.starts))
	}
	options := d.table.starts[0].options
	notebookID := notebook
	if want := (Options{ConnectorID: ownCloud, UserID: owner, Parser: "datalab", NotebookID: &notebookID}); !reflect.DeepEqual(options, want) {
		t.Errorf("options %+v, want %+v", options, want)
	}

	// The rows are committed before any download is handed over.
	if want := []string{"committed", "downloads handed over"}; !reflect.DeepEqual(d.events.list(), want) {
		t.Errorf("order %v, want %v", d.events.list(), want)
	}
	if len(d.handed.jobs) != 3 || d.handed.jobs[2].Path != "/loose.pdf" || d.handed.jobs[2].Name != "loose.pdf" ||
		d.handed.jobs[2].DocumentID != 104 || d.handed.jobs[2].Account.AppPassword != "app-password" {
		t.Errorf("jobs %+v", d.handed.jobs)
	}
}

func TestAnImportThatNamesNoParserGetsTheDefaultByName(t *testing.T) {
	d := newDesk(t, plans())
	for _, body := range []string{
		`{"items": [{"path": "/loose.pdf", "kind": "file"}]}`,
		`{"items": [{"path": "/loose.pdf", "kind": "file"}], "parser": null}`,
	} {
		if got := d.post(importOwn, body); got.status != http.StatusOK {
			t.Fatalf("answered %d %s", got.status, got.body)
		}
	}
	for _, start := range d.table.starts {
		if start.options.Parser != "local" {
			t.Errorf("recorded parser %q, want the default by name, never empty", start.options.Parser)
		}
	}
}

func TestWhatAnImportIsRefusedFor(t *testing.T) {
	one := `"items": [{"path": "/loose.pdf", "kind": "file"}]`
	for _, test := range []struct {
		name, path, body string
		as               principal.Method
		status           int
		code, says       string
		// asksNextcloud: the refusal comes after the pick is listed.
		asksNextcloud bool
	}{
		{name: "somebody else's connection looks like none", path: "/api/v1/connectors/13/nextcloud/import",
			body: `{` + one + `}`, status: 404, says: "Nextcloud connection not found"},
		{name: "no such connection", path: "/api/v1/connectors/99/nextcloud/import",
			body: `{` + one + `}`, status: 404, says: "Nextcloud connection not found"},
		{name: "a connection id that is not a number", path: "/api/v1/connectors/twelve/nextcloud/import",
			body: `{` + one + `}`, status: 422},
		{name: "a connection made for another server", path: "/api/v1/connectors/14/nextcloud/import",
			body: `{` + one + `}`, status: 400, code: "nextcloud_reconnect", says: "for another server; connect again"},
		// Whose documents the picks become is the caller, never the body.
		{name: "a body that names whose documents these are", path: importOwn,
			body: `{"workspace_id": 7, ` + one + `}`, status: 422, says: "JSON object"},
		{name: "no picks", path: importOwn, body: `{"items": []}`, status: 422, says: "Pick at least one"},
		{name: "a kind that is neither", path: importOwn,
			body: `{"items": [{"path": "/a", "kind": "link"}]}`, status: 422, says: `"file" or "folder"`},
		{name: "a key this body does not have", path: importOwn,
			body: `{` + one + `, "processing_mode": "premium"}`, status: 422, says: "JSON object"},
		{name: "not JSON", path: importOwn, body: `items=loose.pdf`, status: 422, says: "JSON object"},
		{name: "the vision switch, with no vision model for the caller or the server", path: importOwn,
			body: `{` + one + `, "use_vision_llm": true}`, status: 400, says: "No vision model is set for this account"},
		{name: "a parser with no key, by name", path: importOwn,
			body: `{` + one + `, "parser": "llamaparse"}`, status: 400, says: "The llamaparse parser is not configured on this server"},
		{name: "the retired word", path: importOwn,
			body: `{` + one + `, "parser": "cloud"}`, status: 400, says: `"cloud" is not a parser`},
		{name: "somebody else's notebook looks like none", path: importOwn,
			body: `{` + one + `, "notebook_id": 41}`, status: 404, says: "Notebook not found"},
		{name: "no such notebook", path: importOwn,
			body: `{` + one + `, "notebook_id": 4040}`, status: 404, says: "Notebook not found"},
		{name: "the whole drive", path: importOwn, asksNextcloud: false,
			body: `{"items": [{"path": "/", "kind": "folder"}]}`, status: 400, says: "not the whole drive"},
		{name: "a picked file that is gone", path: importOwn, asksNextcloud: true,
			body: `{"items": [{"path": "/gone.pdf", "kind": "file"}]}`, status: 404, says: "no longer in Nextcloud"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDesk(t, plans())
			if test.as != "" {
				d.as.Method = test.as
			}
			got := d.post(test.path, test.body)
			if got.status != test.status || !strings.Contains(got.message, test.says) || (test.code != "" && got.code != test.code) {
				t.Fatalf("answered %d %s %q, want %d %s saying %q", got.status, got.code, got.message, test.status, test.code, test.says)
			}
			// Nothing was written and nothing was handed over; and unless
			// the refusal is about the pick itself, Nextcloud was not even
			// asked: a pick is listed only once the import can go ahead.
			if len(d.table.starts) != 0 || len(d.handed.jobs) != 0 {
				t.Error("a refused import wrote or started something")
			}
			if asked := len(d.cloud.requests()); (asked > 0) != test.asksNextcloud {
				t.Errorf("Nextcloud was asked %d times", asked)
			}
		})
	}
}

func TestAPasswordNextcloudRefusesIsNotASignOut(t *testing.T) {
	d := newDesk(t, plans())
	d.cloud.before = func(response http.ResponseWriter, _ *http.Request) bool {
		response.WriteHeader(http.StatusUnauthorized)
		return true
	}
	// A 401 from this API signs the person out of the app; it is Nextcloud
	// that refused them, so it is a 400 with a code the picker reads.
	for _, path := range []string{previewOwn, importOwn} {
		if got := d.post(path, pickPlans); got.status != http.StatusBadRequest || got.code != "nextcloud_auth" {
			t.Errorf("%s answered %d %s", path, got.status, got.code)
		}
	}
	// A Nextcloud that is down is a 502, and its address is not in the answer.
	d.cloud.before = func(response http.ResponseWriter, _ *http.Request) bool {
		response.WriteHeader(http.StatusInternalServerError)
		return true
	}
	if got := d.post(importOwn, pickPlans); got.status != http.StatusBadGateway || strings.Contains(got.body, "127.0.0.1") {
		t.Errorf("answered %d %s", got.status, got.body)
	}
}

func TestAPickThatIsTooBigImportsNothingAndSaysHowMany(t *testing.T) {
	files := map[string]node{"/Big": {folder: true}}
	for index := 0; index <= MaxImportFiles; index++ {
		files["/Big/"+itoa(index)+".pdf"] = node{content: "x"}
	}
	d := newDesk(t, files)
	body := `{"items": [{"path": "/Big", "kind": "folder"}]}`

	// The preview says so without refusing: the picker shows the count.
	if got := d.post(previewOwn, body); got.status != http.StatusOK || !strings.Contains(got.body, `"over_limit":true`) {
		t.Errorf("the preview answered %d %s", got.status, got.body)
	}
	got := d.post(importOwn, body)
	if got.status != http.StatusUnprocessableEntity || got.code != "too_many_files" || got.message != "Pick at most 200 files at a time" ||
		!strings.Contains(got.body, `"max_files":200`) {
		t.Errorf("answered %d %s", got.status, got.body)
	}
	if len(d.table.starts) != 0 {
		t.Error("part of a pick that was too big was imported")
	}
}

func TestADocumentBeingDeletedIsNotImportedOver(t *testing.T) {
	d := newDesk(t, plans())
	d.table.startErr = &errBeingDeleted{name: "loose.pdf"}
	got := d.post(importOwn, `{"items": [{"path": "/loose.pdf", "kind": "file"}]}`)
	if got.status != http.StatusConflict || !strings.Contains(got.message, `"loose.pdf" is being deleted`) {
		t.Errorf("answered %d %q", got.status, got.message)
	}
	if len(d.handed.jobs) != 0 {
		t.Error("downloads were started for an import that was not recorded")
	}
}

// imported puts a document in the table as an earlier import left it.
func (d *desk) imported(id int64, state, stage string, stale bool, etag string) {
	d.table.documents[id] = &held{owner: owner, imported: Imported{
		Title: "Renamed by its reader", State: state, Stage: stage, Stale: stale,
		ConnectorID: 5, ServerURL: d.cloud.server.URL, Username: "ada", Path: "/loose.pdf", ETag: etag,
	}}
}

func TestRefreshingAnImportedFile(t *testing.T) {
	const refresh = "/api/v1/connectors/12/nextcloud/documents/500/refresh"
	for _, test := range []struct {
		name         string
		state, stage string
		stale        bool
		etag         string
		status       int
		body         string
		downloads    bool
	}{
		{name: "unchanged in Nextcloud", state: "ready", etag: "el", status: 200, body: `{"status":"unchanged"}`},
		{name: "changed in Nextcloud", state: "ready", etag: "older", status: 200, body: `{"status":"queued"}`, downloads: true},
		{name: "failed before, even if unchanged", state: "failed", etag: "el", status: 200, body: `{"status":"queued"}`, downloads: true},
		{name: "already being fetched: no second job", state: "pending", stage: "downloading", etag: "older", status: 200, body: `{"status":"queued"}`},
		{name: "already being indexed: no second job", state: "processing", etag: "older", status: 200, body: `{"status":"queued"}`},
		{name: "a download abandoned by a process that died is started again", state: "pending", stage: "downloading", stale: true, etag: "el",
			status: 200, body: `{"status":"queued"}`, downloads: true},
		{name: "being deleted looks like no document", state: "deleting", etag: "el", status: 404, body: "Imported Nextcloud file not found"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDesk(t, plans())
			d.imported(500, test.state, test.stage, test.stale, test.etag)
			got := d.post(refresh, "")
			if got.status != test.status || !strings.Contains(got.body, test.body) {
				t.Fatalf("answered %d %s, want %d %s", got.status, got.body, test.status, test.body)
			}
			if started := len(d.handed.jobs) == 1; started != test.downloads {
				t.Fatalf("downloads handed over: %+v", d.handed.jobs)
			}
			if !test.downloads {
				return
			}
			// The file is fetched under its name in Nextcloud, not the
			// title somebody gave the document: the name's extension is
			// what the indexer picks a parser by.
			job := d.handed.jobs[0]
			if job.DocumentID != 500 || job.Path != "/loose.pdf" || job.Name != "loose.pdf" || job.UserID != owner {
				t.Errorf("job %+v", job)
			}
			// The document waits for its download and knows the new etag;
			// it was imported through a connection row that is gone (5),
			// and now records the one that refreshed it.
			after := d.table.document(500).imported
			if after.State != "pending" || after.Stage != "downloading" || after.ETag != "el" || after.ConnectorID != ownCloud {
				t.Errorf("the document is %+v", after)
			}
		})
	}
}

func TestWhatARefreshIsRefusedFor(t *testing.T) {
	for _, test := range []struct {
		name, path string
		change     func(d *desk)
		status     int
		code, says string
	}{
		{name: "no such document", path: "/api/v1/connectors/12/nextcloud/documents/404/refresh", status: 404, says: "Imported Nextcloud file not found"},
		{name: "a document id that is not a number", path: "/api/v1/connectors/12/nextcloud/documents/x/refresh", status: 422},
		{name: "somebody else's connection", path: "/api/v1/connectors/13/nextcloud/documents/500/refresh", status: 404, says: "Nextcloud connection not found"},
		{name: "a connection made for another server", path: "/api/v1/connectors/14/nextcloud/documents/500/refresh", status: 400, code: "nextcloud_reconnect"},
		{name: "a document that was not imported from Nextcloud", path: "/api/v1/connectors/12/nextcloud/documents/500/refresh",
			change: func(d *desk) { d.table.documents[500].imported.Path = "" }, status: 404, says: "Imported Nextcloud file not found"},
		{name: "a document imported from another account", path: "/api/v1/connectors/12/nextcloud/documents/500/refresh",
			change: func(d *desk) { d.table.documents[500].imported.Username = "bob" }, status: 404, says: "Imported Nextcloud file not found"},
		// The other account imported the very same Nextcloud file (same
		// server, user and path, as a shared drive would have it): it is
		// still not the caller's document, and is answered as no document.
		{name: "somebody else's document", path: "/api/v1/connectors/12/nextcloud/documents/500/refresh",
			change: func(d *desk) { d.table.documents[500].owner = somebody }, status: 404, says: "Imported Nextcloud file not found"},
		{name: "the file is gone from Nextcloud", path: "/api/v1/connectors/12/nextcloud/documents/500/refresh",
			change: func(d *desk) { d.table.documents[500].imported.Path = "/gone.pdf" }, status: 404, says: "no longer in Nextcloud"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDesk(t, plans())
			d.imported(500, "ready", "", false, "older")
			if test.change != nil {
				test.change(d)
			}
			got := d.post(test.path, "")
			if got.status != test.status || !strings.Contains(got.message, test.says) || (test.code != "" && got.code != test.code) {
				t.Fatalf("answered %d %s %q, want %d %s saying %q", got.status, got.code, got.message, test.status, test.code, test.says)
			}
			if len(d.handed.jobs) != 0 || d.table.document(500).imported.State != "ready" {
				t.Error("a refused refresh changed the document or started a download")
			}
		})
	}
}

// Two accounts, every route. Account B holds a working connection of its
// own to the same Nextcloud, and still cannot preview or import through
// account A's connection, link an import to A's notebook, or refresh A's
// document. Each is answered exactly as the same request about an id that
// was never used, so B learns nothing about what A has, and nothing of A's
// is written or downloaded.
func TestOneAccountCannotReachAnothersConnectionNotebookOrDocument(t *testing.T) {
	const one = `{"items": [{"path": "/loose.pdf", "kind": "file"}]}`
	for _, test := range []struct {
		name          string
		theirs, never string // the request about A's row, and about no row
		body          string
	}{
		{name: "preview through A's connection",
			theirs: previewOwn, never: "/api/v1/connectors/9999/nextcloud/import/preview", body: one},
		{name: "import through A's connection",
			theirs: importOwn, never: "/api/v1/connectors/9999/nextcloud/import", body: one},
		{name: "refresh A's document through B's own connection",
			theirs: "/api/v1/connectors/13/nextcloud/documents/500/refresh",
			never:  "/api/v1/connectors/13/nextcloud/documents/9999/refresh"},
		{name: "refresh A's document through A's connection",
			theirs: "/api/v1/connectors/12/nextcloud/documents/500/refresh",
			never:  "/api/v1/connectors/9999/nextcloud/documents/500/refresh"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newDesk(t, plans())
			d.imported(500, "ready", "", false, "older") // A's document
			d.as.UserID = somebody

			theirs, never := d.post(test.theirs, test.body), d.post(test.never, test.body)
			if theirs.status != http.StatusNotFound {
				t.Fatalf("answered %d %q, want a 404", theirs.status, theirs.message)
			}
			if theirs.status != never.status || theirs.message != never.message || theirs.code != never.code {
				t.Errorf("A's row answered %d %q and a row never made %d %q; they must be one answer",
					theirs.status, theirs.message, never.status, never.message)
			}
			if len(d.table.starts) != 0 || len(d.handed.jobs) != 0 || d.table.document(500).imported.State != "ready" {
				t.Error("the refused request wrote, started a download, or changed A's document")
			}
		})
	}

	// B's own connection with A's notebook: the import is refused before
	// Nextcloud is asked, and is the answer for a notebook never made.
	t.Run("an import linked to A's notebook", func(t *testing.T) {
		d := newDesk(t, plans())
		d.as.UserID = somebody
		const through = "/api/v1/connectors/13/nextcloud/import"
		theirs := d.post(through, `{"items": [{"path": "/loose.pdf", "kind": "file"}], "notebook_id": 40}`)
		never := d.post(through, `{"items": [{"path": "/loose.pdf", "kind": "file"}], "notebook_id": 4040}`)
		if theirs.status != http.StatusNotFound || theirs.message != "Notebook not found" || theirs.message != never.message {
			t.Fatalf("answered %d %q (and %d %q for no notebook)", theirs.status, theirs.message, never.status, never.message)
		}
		if len(d.table.starts) != 0 || len(d.cloud.requests()) != 0 {
			t.Error("the refused import reached the database or Nextcloud")
		}
		// And B's own import, with no notebook, is B's.
		if got := d.post(through, one); got.status != http.StatusOK || d.table.starts[0].options.UserID != somebody {
			t.Fatalf("B's own import answered %d %s", got.status, got.body)
		}
		for _, job := range d.handed.jobs {
			if job.UserID != somebody {
				t.Errorf("a download for B's import is owed to %s", job.UserID)
			}
		}
	})
}

func TestAnImportWithVisionOnTakesPicturesAndStoresTheSwitch(t *testing.T) {
	d := newDesk(t, plans())
	d.vision.refusal = ""
	// The local parser does not read .png; with vision on the picture is
	// the vision model's to read.
	got := d.post(importOwn, `{"items": [{"path": "/Plans", "kind": "folder"}], "parser": "local", "use_vision_llm": true}`)
	if got.status != http.StatusOK {
		t.Fatalf("answered %d %s", got.status, got.body)
	}
	if !strings.Contains(got.body, `"unsupported":[]`) || len(d.table.starts) != 1 || !d.table.starts[0].options.Vision {
		t.Fatalf("answered %s; options %+v", got.body, d.table.starts)
	}
}
