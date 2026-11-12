package uploads

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice/parserchoicetest"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/uploadvision"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_filestore"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

// Two accounts. Every test runs as callerID unless it says otherwise;
// otherID is the second account of the isolation tests.
const (
	callerID = "11111111-2222-4333-8444-555555555555"
	otherID  = "99999999-8888-4777-8666-555555555555"
)

// document is a row of the stand-in database.
type document struct {
	id       int64
	identity string
	title    string
	state    string
	parser   string
	// vision is the upload's use_vision_llm, as the row stores it.
	vision bool
	// owner is the account the document was recorded for.
	owner string
	// originals are the document's file rows, oldest first.
	originals []Staged
	// kind is the document type; folder is the folder id it was placed in
	// (0 for none) and relative its path inside an uploaded folder.
	kind     string
	folder   int64
	relative string
	// touched counts the times an unchanged folder file was seen again.
	touched int
}

// rows is the database for one test. It keeps the rule Record keeps (decide,
// and the check that the document is still the one the key was built for)
// by calling the same functions, so the route is tested against the real
// decision and only the SQL is stood in for. The SQL itself is tested
// against Postgres in database_test.go.
type rows struct {
	mu        sync.Mutex
	nextID    int64
	documents map[string]*document
	// recordErr fails the transaction.
	recordErr error
	// events is the order things happened in, for the tests that care.
	events *[]string
}

func (r *rows) NextDocumentID(context.Context) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	return r.nextID, nil
}

// prior is the account's document of this identity. The owner is asked for
// as the statement asks for it, and not left to the hash, so a route that
// passed the wrong account would be caught here.
func (r *rows) prior(userID, identity string) *Prior {
	held, found := r.documents[identity]
	if !found || held.owner != userID {
		return nil
	}
	prior := &Prior{ID: held.id, State: held.state}
	if len(held.originals) > 0 {
		prior.Checksum = held.originals[len(held.originals)-1].SHA256
	}
	return prior
}

func (r *rows) UploadNamed(_ context.Context, userID, identity string) (*Prior, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.prior(userID, identity), nil
}

func (r *rows) Record(_ context.Context, batch Batch) ([]Recorded, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.recordErr != nil {
		return nil, r.recordErr
	}
	// Decided for the whole batch before anything is written, as a
	// transaction would have it.
	recorded := make([]Recorded, len(batch.Files))
	for index, file := range batch.Files {
		prior := r.prior(batch.UserID, file.Identity)
		outcome := decide(prior, file.SHA256)
		if outcome == BeingDeleted {
			return nil, &ErrBeingDeleted{Name: file.Name}
		}
		recorded[index] = Recorded{DocumentID: file.DocumentID, Outcome: outcome}
		if prior != nil {
			recorded[index].DocumentID = prior.ID
		}
	}
	for index, file := range batch.Files {
		switch recorded[index].Outcome {
		case Created:
			r.documents[file.Identity] = &document{
				id: file.DocumentID, identity: file.Identity, title: file.Name, state: "pending",
				parser: batch.Parser, vision: batch.Vision, owner: batch.UserID, originals: []Staged{file},
			}
		case Replaced:
			held := r.documents[file.Identity]
			held.state, held.parser, held.vision = "pending", batch.Parser, batch.Vision
			held.originals = append(held.originals, file)
		case Retried:
			held := r.documents[file.Identity]
			held.state, held.parser, held.vision = "pending", batch.Parser, batch.Vision
		}
	}
	if r.events != nil {
		*r.events = append(*r.events, "recorded")
	}
	return recorded, nil
}

// queue is the indexing queue for one test.
type queue struct {
	mu     sync.Mutex
	queued []int64
	// down makes every enqueue fail, as a broker that does not answer.
	down   bool
	rows   *rows
	events *[]string
}

func (q *queue) Enqueue(_ context.Context, documentID int64, logContext produce.LogContext) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.down {
		// What indexqueue.Indexing does: the document is marked failed.
		for _, held := range q.rows.documents {
			if held.id == documentID {
				held.state = "failed"
			}
		}
		return errors.New("the broker did not answer")
	}
	q.queued = append(q.queued, documentID)
	if q.events != nil {
		*q.events = append(*q.events, "queued")
	}
	return nil
}

// site is the route on a real listener, with a real file store in a
// temporary directory. A real connection matters here: the route moves the
// connection's read deadline, which a recorder does not have.
type site struct {
	t      testing.TB
	server *httptest.Server
	root   string
	store  *filestore.Local
	rows   *rows
	// tree is the folder upload's database: the same documents, and the
	// folders.
	tree  *tree
	queue *queue
	// as is who the gate would have resolved; tests change it.
	as principal.Principal
	// vision is whether the caller has a vision model; tests change it.
	vision *gate
}

// gate is the vision check (uploadvision.Vision): the refusal it gives,
// "" for an account that has a vision model, and who it was asked about.
type gate struct {
	refusal string
	asked   []string
}

func (g *gate) Refusal(_ context.Context, userID string) (string, error) {
	g.asked = append(g.asked, userID)
	return g.refusal, nil
}

// limits small enough that a test can pass them with a few bytes.
var testLimits = parserchoice.Limits{MaxFileBytes: 1 << 10, MaxFiles: 3}

func newSite(t *testing.T) *site { return newSiteWith(t, testLimits) }

// newSiteWith is a site with limits of the caller's choosing. It takes a
// testing.TB so the benchmark can use it.
func newSiteWith(t testing.TB, limits parserchoice.Limits) *site {
	return newSitePaced(t, limits, 5*time.Second, 0)
}

// newSitePaced is a site whose server has the read and write timeouts
// cmd/api's has (serverTimeout; zero for none), and a route that lets a
// client stall for `stall`.
func newSitePaced(t testing.TB, limits parserchoice.Limits, stall, serverTimeout time.Duration) *site {
	t.Helper()
	root := t.TempDir()
	store, err := filestore.OpenLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	held := &rows{nextID: 100, documents: map[string]*document{}}
	s := &site{
		t: t, root: root, store: store, rows: held, queue: &queue{rows: held},
		as: principal.Principal{UserID: callerID, Method: principal.MethodSession},
		// No vision model, until a test gives the caller one.
		vision: &gate{refusal: uploadvision.NoModel},
	}
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	// Local is the default; Datalab is configured; LlamaParse and
	// Unstructured have no key.
	parsers := parserchoicetest.Registry(t, parse.Local, parse.Datalab)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	Route{
		Rows: held, Files: store, Indexing: s.queue, Vision: s.vision,
		Parsers: parsers, Limits: limits, Stall: stall, Logger: logger,
	}.Register(api)
	// The folder upload beside it, over the same database, store and queue.
	s.tree = &tree{rows: held, nextID: 500, byPath: map[string]int64{}, watched: map[int64]string{}}
	FolderRoute{
		Rows: s.tree, Files: store, Indexing: s.queue, Vision: s.vision,
		Parsers: parsers, Limits: limits, Stall: stall, Logger: logger,
	}.Register(api)
	s.server = httptest.NewUnstartedServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		request = request.WithContext(principal.WithPrincipal(request.Context(), s.as))
		api.Adapter().ServeHTTP(response, request)
	}))
	s.server.Config.ReadTimeout, s.server.Config.WriteTimeout = serverTimeout, serverTimeout
	s.server.Start()
	t.Cleanup(s.server.Close)
	return s
}

// part is one part of a form, in the order it is sent.
type part struct {
	// field and value make a text field; file and content make a file.
	field, value string
	file         string
	content      []byte
}

func field(name, value string) part { return part{field: name, value: value} }
func file(name, content string) part {
	return part{file: name, content: []byte(content)}
}

// form builds a multipart body from parts, in order.
func form(t testing.TB, parts ...part) (io.Reader, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, piece := range parts {
		if piece.file == "" {
			if err := writer.WriteField(piece.field, piece.value); err != nil {
				t.Fatal(err)
			}
			continue
		}
		target, err := writer.CreateFormFile(fieldFiles, piece.file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := target.Write(piece.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, writer.FormDataContentType()
}

// reply is what the route answered.
type reply struct {
	status int
	// answer is the body of a 200.
	answer answer
	// message is the error envelope's message for anything else.
	message string
}

func (s *site) post(body io.Reader, contentType string) reply {
	s.t.Helper()
	response, err := http.Post(s.server.URL+Path, contentType, body)
	if err != nil {
		s.t.Fatalf("posting: %v", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		s.t.Fatal(err)
	}
	got := reply{status: response.StatusCode}
	if response.StatusCode == http.StatusOK {
		if err := json.Unmarshal(raw, &got.answer); err != nil {
			s.t.Fatalf("the answer is not JSON: %v: %s", err, raw)
		}
		return got
	}
	var envelope struct {
		Error struct {
			Message   string `json:"message"`
			Status    int    `json:"status"`
			RequestID string `json:"request_id"`
			Code      string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Error.Status != response.StatusCode {
		s.t.Fatalf("a refusal that is not the error envelope: %s", raw)
	}
	got.message = envelope.Error.Message
	return got
}

func (s *site) upload(parts ...part) reply {
	s.t.Helper()
	body, contentType := form(s.t, parts...)
	return s.post(body, contentType)
}

// stored is every object in the store, by key.
func (s *site) stored() map[string][]byte {
	s.t.Helper()
	objects := map[string][]byte{}
	err := filepath.WalkDir(s.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		key, err := filepath.Rel(s.root, path)
		if err != nil {
			return err
		}
		objects[filepath.ToSlash(key)] = content
		return nil
	})
	if err != nil {
		s.t.Fatal(err)
	}
	return objects
}

// titled is the document of that title, or nil.
func (s *site) titled(title string) *document {
	for _, held := range s.rows.documents {
		if held.title == title {
			return held
		}
	}
	return nil
}

func digest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}
