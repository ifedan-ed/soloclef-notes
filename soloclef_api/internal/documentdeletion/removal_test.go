package documentdeletion

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// world is a document and everything stored for it: its rows, its points in
// the vector store, and its objects in a real file store on disk. Every
// step of a removal is written to `log`, and `failAt` makes one step fail
// once, as a service that did not answer.
type world struct {
	t *testing.T
	// state is the document's status state; "" means the row is gone.
	state  string
	chunks []int64
	points map[int64]bool
	files  []StoredFile
	store  *filestore.Local
	stores *filestore.Stores

	log    []string
	failAt string
}

func newWorld(t *testing.T, chunks int, state string) *world {
	t.Helper()
	store, err := filestore.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stores, err := filestore.NewStores(store)
	if err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, state: state, points: map[int64]bool{}, store: store, stores: stores}
	for id := int64(1); id <= int64(chunks); id++ {
		w.chunks = append(w.chunks, id)
		w.points[id] = true
	}
	for _, key := range []string{"documents/7/42/original/aaaa.pdf", "documents/7/42/original/bbbb.pdf"} {
		if err := store.Put(context.Background(), key, []byte("bytes"), "application/pdf"); err != nil {
			t.Fatal(err)
		}
		w.files = append(w.files, StoredFile{Backend: "local", Key: key})
	}
	return w
}

// step logs one step, and fails it if it is the one the test chose.
func (w *world) step(name string) error {
	if w.failAt == name {
		w.failAt = ""
		w.log = append(w.log, name+" FAILED")
		return errors.New(name + " did not answer")
	}
	w.log = append(w.log, name)
	return nil
}

func (w *world) Mark(context.Context, int64) (bool, error) {
	if w.state == "" {
		return false, nil
	}
	if err := w.step("mark"); err != nil {
		return false, err
	}
	w.state = "deleting"
	return true, nil
}

func (w *world) ChunkIDs(_ context.Context, _ int64, batch int) ([]int64, error) {
	return append([]int64(nil), w.chunks[:min(batch, len(w.chunks))]...), nil
}

func (w *world) DeleteChunks(_ context.Context, _ int64, ids []int64) error {
	// A row must never be deleted while its point is still stored: the
	// point could not be found again.
	for _, id := range ids {
		if w.points[id] {
			w.t.Errorf("chunk %d's row is being deleted while its point is still in the vector store", id)
		}
	}
	if err := w.step(fmt.Sprintf("rows %d", len(ids))); err != nil {
		return err
	}
	w.chunks = w.chunks[len(ids):]
	return nil
}

func (w *world) Delete(_ context.Context, ids []int64) error {
	if w.state != "deleting" {
		w.t.Error("points are being removed from a document that search can still return")
	}
	if err := w.step(fmt.Sprintf("points %d", len(ids))); err != nil {
		return err
	}
	for _, id := range ids {
		delete(w.points, id)
	}
	return nil
}

func (w *world) Files(context.Context, int64) ([]StoredFile, error) {
	if len(w.chunks) != 0 {
		w.t.Error("the files were read before the chunks were gone")
	}
	return w.files, w.step("files")
}

func (w *world) DeleteDocument(context.Context, int64) error {
	// The row is the only record of the keys: it goes after the objects.
	if left := w.objects(); left != 0 {
		w.t.Errorf("the document row is being deleted while %d of its objects are still stored", left)
	}
	if err := w.step("document"); err != nil {
		return err
	}
	w.state, w.files = "", nil
	return nil
}

// Named is the file stores, with a step in front so a test can fail it.
func (w *world) Named(backend string) (filestore.Store, error) {
	if err := w.step("store"); err != nil {
		return nil, err
	}
	return w.stores.Named(backend)
}

// objects is how many of the document's files are still in the store.
func (w *world) objects() int {
	stored := 0
	for _, key := range []string{"documents/7/42/original/aaaa.pdf", "documents/7/42/original/bbbb.pdf"} {
		if _, err := w.store.Stat(context.Background(), key); err == nil {
			stored++
		}
	}
	return stored
}

func (w *world) removal() Removal { return Removal{Rows: w, Vectors: w, Files: w} }

func (w *world) gone() bool {
	return w.state == "" && len(w.chunks) == 0 && len(w.points) == 0 && w.objects() == 0
}

func TestARemovalTakesEverythingInItsOrder(t *testing.T) {
	// Two and a half batches of chunks.
	w := newWorld(t, 2*chunkBatch+500, "ready")
	if err := w.removal().Remove(context.Background(), 42); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"mark",
		"points 1000", "rows 1000",
		"points 1000", "rows 1000",
		"points 500", "rows 500",
		"files", "store", "store",
		"document",
	}
	if !reflect.DeepEqual(w.log, want) {
		t.Errorf("the removal did\n%v\nwant\n%v", w.log, want)
	}
	if !w.gone() {
		t.Errorf("left behind: state %q, %d chunks, %d points, %d objects", w.state, len(w.chunks), len(w.points), w.objects())
	}
}

// Whatever step fails, the document is left marked (out of search), nothing
// is orphaned, and asking again finishes the job.
func TestARemovalThatFailsAnywhereIsFinishedByAskingAgain(t *testing.T) {
	for _, failing := range []string{"mark", "points 1000", "rows 1000", "points 200", "rows 200", "files", "store", "document"} {
		t.Run("failing at "+failing, func(t *testing.T) {
			w := newWorld(t, chunkBatch+200, "ready")
			w.failAt = failing

			err := w.removal().Remove(context.Background(), 42)
			if err == nil {
				t.Fatal("the failure was swallowed")
			}
			// The cause is in the error for the log.
			if !strings.Contains(err.Error(), "did not answer") || !strings.Contains(err.Error(), "document 42") {
				t.Errorf("error %q does not say what failed for which document", err)
			}
			if failing != "mark" && w.state != "deleting" {
				t.Errorf("the document is %q after a failed removal; it must stay out of search", w.state)
			}
			// A point with no row cannot be found again; a stored object
			// with no row cannot either.
			for id := range w.points {
				if !contains(w.chunks, id) {
					t.Errorf("point %d is stored and its row is gone", id)
				}
			}
			if w.state == "" && w.objects() != 0 {
				t.Errorf("the row is gone and %d objects are still stored", w.objects())
			}

			if err := w.removal().Remove(context.Background(), 42); err != nil {
				t.Fatalf("the second attempt: %v", err)
			}
			if !w.gone() {
				t.Errorf("after the second attempt: state %q, %d chunks, %d points, %d objects", w.state, len(w.chunks), len(w.points), w.objects())
			}
		})
	}
}

func TestRemovingWhatIsAlreadyGoneIsNotAnError(t *testing.T) {
	w := newWorld(t, 3, "ready")
	for attempt := 1; attempt <= 2; attempt++ {
		if err := w.removal().Remove(context.Background(), 42); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
	}
	if !w.gone() {
		t.Error("the document is not gone")
	}
}

func TestAFileInAStoreThisProcessCannotReachKeepsTheDocument(t *testing.T) {
	w := newWorld(t, 0, "ready")
	w.files = append(w.files, StoredFile{Backend: "s3", Key: "documents/7/42/original/cccc.pdf"})

	err := w.removal().Remove(context.Background(), 42)
	if !errors.Is(err, filestore.ErrBackendNotRead) {
		t.Fatalf("error %v, want the store's own refusal", err)
	}
	// The row must survive: it is the only record of the object's key.
	if w.state != "deleting" {
		t.Errorf("the document is %q; deleting its row would orphan the object for good", w.state)
	}
}

func contains(ids []int64, wanted int64) bool {
	for _, id := range ids {
		if id == wanted {
			return true
		}
	}
	return false
}

// --- the route ---

// The two accounts of the route's tests.
const (
	owner    = "11111111-2222-4333-8444-555555555555"
	stranger = "99999999-2222-4333-8444-555555555555"
)

// shelf is whose each document is, and what was removed.
type shelf struct {
	documents map[int64]string
	asked     []string
	removed   []int64
	err       error
}

func (s *shelf) Owns(_ context.Context, documentID int64, userID string) (bool, error) {
	s.asked = append(s.asked, userID)
	return s.documents[documentID] == userID && userID != "", nil
}

func (s *shelf) Remove(_ context.Context, documentID int64) error {
	if s.err != nil {
		return s.err
	}
	s.removed = append(s.removed, documentID)
	delete(s.documents, documentID)
	return nil
}

func TestTheDeleteRoute(t *testing.T) {
	for _, test := range []struct {
		name, id string
		as       string
		broken   bool
		status   int
		says     string
		removed  bool
	}{
		{name: "the caller's own document", id: "42", status: 200, says: `"message":"Document deleted successfully"`, removed: true},
		{name: "an id that names nothing", id: "404", status: 404, says: "Document with id 404 not found"},
		// Ids are bigint: a number past int32 is an ordinary id that
		// happens to name nothing, not an error.
		{name: "an id past int32", id: "99999999999", status: 404, says: "Document with id 99999999999 not found"},
		{name: "an id below one", id: "0", status: 404, says: "not found"},
		{name: "an id that is not a number", id: "forty-two", status: 422},
		{name: "a removal that stopped partway says to ask again", id: "42", broken: true, status: 503, says: "Delete it again to finish"},
		// The isolation rule: another account's document is answered word
		// for word as an id that names nothing, and nothing is removed.
		{name: "another account's document looks like no document", id: "42", as: stranger, status: 404, says: "Document with id 42 not found"},
	} {
		t.Run(test.name, func(t *testing.T) {
			held := &shelf{documents: map[int64]string{42: owner, 45: stranger}}
			if test.broken {
				held.err = errors.New("the vector store at http://qdrant:6333 did not answer")
			}
			api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
			Route{Owners: held, Removal: held,
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}.Register(api)

			caller := owner
			if test.as != "" {
				caller = test.as
			}
			request := httptest.NewRequest(http.MethodDelete, "/api/v1/documents/"+test.id, nil)
			request = request.WithContext(principal.WithPrincipal(request.Context(),
				principal.Principal{UserID: caller, Method: principal.MethodSession}))
			recorder := httptest.NewRecorder()
			api.Adapter().ServeHTTP(recorder, request)

			if recorder.Code != test.status || !strings.Contains(recorder.Body.String(), test.says) {
				t.Fatalf("answered %d %s, want %d saying %q", recorder.Code, recorder.Body, test.status, test.says)
			}
			if removed := len(held.removed) == 1; removed != test.removed {
				t.Errorf("removed %v", held.removed)
			}
			// The owner asked about is always the caller, never anything
			// the request carried.
			for _, asked := range held.asked {
				if asked != caller {
					t.Errorf("ownership was asked about %q, and the caller is %q", asked, caller)
				}
			}
			// What failed is for the log, never for the caller.
			if strings.Contains(recorder.Body.String(), "qdrant") {
				t.Errorf("the answer names an internal address: %s", recorder.Body)
			}
		})
	}
}

// The state a document is in refuses nothing: one that is stuck processing,
// or was left "deleting" by an attempt that failed, is deleted like any
// other.
func TestADocumentInAnyStateCanBeDeleted(t *testing.T) {
	for _, state := range []string{"pending", "processing", "failed", "deleting", "ready"} {
		w := newWorld(t, 3, state)
		if err := w.removal().Remove(context.Background(), 42); err != nil {
			t.Fatalf("%s: %v", state, err)
		}
		if !w.gone() {
			t.Errorf("a %s document was not removed", state)
		}
	}
}

func TestTheVectorStoreIsAskedToRemoveExactlyTheChunksPoints(t *testing.T) {
	var asked struct {
		path, key, body string
	}
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		raw, _ := io.ReadAll(request.Body)
		asked.path, asked.key, asked.body = request.Method+" "+request.URL.RequestURI(), request.Header.Get("api-key"), string(raw)
		response.WriteHeader(status)
		_, _ = io.WriteString(response, `{"status":"ok"}`)
	}))
	defer server.Close()
	vectors := Qdrant{URL: server.URL + "/", APIKey: "secret", Collection: "chunks live", HTTP: server.Client()}
	if err := vectors.Check(); err != nil {
		t.Fatal(err)
	}

	if err := vectors.Delete(context.Background(), []int64{11, 12, 13}); err != nil {
		t.Fatal(err)
	}
	// wait=true: the rows are deleted next, so the points must be gone, not
	// merely queued for removal.
	if asked.path != "POST /collections/chunks%20live/points/delete?wait=true" || asked.key != "secret" || asked.body != `{"points":[11,12,13]}` {
		t.Errorf("asked %+v", asked)
	}

	// Nothing to remove asks nothing.
	asked.path = ""
	if err := vectors.Delete(context.Background(), nil); err != nil || asked.path != "" {
		t.Errorf("an empty batch asked %q, %v", asked.path, err)
	}

	// A refusal is an error, so the rows of that batch are kept.
	status = http.StatusServiceUnavailable
	if err := vectors.Delete(context.Background(), []int64{11}); err == nil || !strings.Contains(err.Error(), "503") {
		t.Errorf("a 503 from the vector store was taken as success: %v", err)
	}

	for name, broken := range map[string]Qdrant{
		"no address":    {Collection: "c", HTTP: http.DefaultClient},
		"no collection": {URL: "http://x", HTTP: http.DefaultClient},
		"no client":     {URL: "http://x", Collection: "c"},
	} {
		if broken.Check() == nil {
			t.Errorf("a vector store with %s passed its check", name)
		}
	}
}
