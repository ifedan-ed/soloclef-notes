package folders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// The route tests, over rows held in memory. database_test.go runs the
// same cases over Postgres when a server is named, which is where the
// statements themselves are held to "another account's row does not exist".
func TestTheRoutes(t *testing.T) {
	routeCases(t, func(*testing.T) world { return newMemory() })
}

// site is the eleven routes mounted as cmd/api mounts them, over one world.
type site struct {
	t      *testing.T
	world  world
	server *httptest.Server
	queue  *recordingQueue
	told   *recordingArrivals
	clock  *steppingClock
}

// recordingQueue keeps what was enqueued, or refuses when it is down.
type recordingQueue struct {
	mu       sync.Mutex
	down     bool
	messages []produce.Message
}

func (q *recordingQueue) Enqueue(_ context.Context, message produce.Message) (string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.down {
		return "", errors.New("the queue is down")
	}
	q.messages = append(q.messages, message)
	return "task", nil
}

// recordingArrivals keeps what was announced, or fails when told to.
type recordingArrivals struct {
	mu       sync.Mutex
	failing  bool
	arrivals []Arrival
}

func (r *recordingArrivals) DocumentEnteredFolder(_ context.Context, arrival Arrival, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failing {
		return errors.New("nothing is listening")
	}
	r.arrivals = append(r.arrivals, arrival)
	return nil
}

// steppingClock gives each reading a later second, so a write's
// `updated_at` can be told from the one before it.
type steppingClock struct {
	mu   sync.Mutex
	last time.Time
}

func (c *steppingClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.last = c.last.Add(time.Second)
	return c.last
}

// callerHeader carries the account a test request is made as. The real
// gate sets the principal from a credential; the test's stands in for it.
const callerHeader = "X-Test-Caller"

func newSite(t *testing.T, w world) *site {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	envelope := errorenvelope.Writer{ReportURL: "https://example.test/contact"}
	s := &site{t: t, world: w, queue: &recordingQueue{}, told: &recordingArrivals{},
		clock: &steppingClock{last: time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)}}

	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "0.0.1"))
	ReadRoutes{Folders: w, Errors: envelope, Logger: logger}.Register(api)
	ListRoute{Folders: w, Errors: envelope, Logger: logger}.Register(api)
	CreateRoute{Folders: w, Errors: envelope, Logger: logger, Now: s.clock.now}.Register(api)
	RenameRoute{Folders: w, Errors: envelope, Logger: logger, Now: s.clock.now}.Register(api)
	MoveRoute{Folders: w, Errors: envelope, Logger: logger, Now: s.clock.now}.Register(api)
	ReorderRoute{Folders: w, Errors: envelope, Logger: logger, Now: s.clock.now}.Register(api)
	WatchRoute{Folders: w, Errors: envelope, Logger: logger, Now: s.clock.now}.Register(api)
	DeleteRoute{Folders: w, Queue: s.queue, Errors: envelope, Logger: logger}.Register(api)
	DocumentMoveRoute{Folders: w, Arrivals: s.told, Errors: envelope, Logger: logger, Now: s.clock.now}.Register(api)
	BulkMoveRoute{Folders: w, Arrivals: s.told, Errors: envelope, Logger: logger, Now: s.clock.now}.Register(api)

	withCaller := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mux.ServeHTTP(response, request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: request.Header.Get(callerHeader), Method: principal.MethodSession})))
	})
	bodies := requestbody.Middleware{
		Covers: func(request *http.Request) bool { return request.Method != http.MethodGet },
	}
	s.server = httptest.NewServer(requestcontext.Middleware(logger, bodies.Wrap(withCaller)))
	t.Cleanup(s.server.Close)
	return s
}

// answered is one response.
type answered struct {
	status int
	raw    []byte
}

// ask sends one request as an account. A body is sent as JSON; an empty
// one sends none.
func (s *site) ask(userID, verb, path, body string) answered {
	s.t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequest(verb, s.server.URL+path, reader)
	if err != nil {
		s.t.Fatal(err)
	}
	request.Header.Set(callerHeader, userID)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := s.server.Client().Do(request)
	if err != nil {
		s.t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		s.t.Fatal(err)
	}
	return answered{status: response.StatusCode, raw: raw}
}

// folderAnswer is a folder as a client reads it.
type folderAnswer struct {
	ID        int64          `json:"id"`
	Name      string         `json:"name"`
	Position  string         `json:"position"`
	ParentID  *int64         `json:"parent_id"`
	CreatedAt string         `json:"created_at"`
	UpdatedAt string         `json:"updated_at"`
	Metadata  map[string]any `json:"metadata"`
}

func (a answered) folder(t *testing.T) folderAnswer {
	t.Helper()
	if a.status != http.StatusOK {
		t.Fatalf("answered %d, want a folder: %s", a.status, a.raw)
	}
	var folder folderAnswer
	if err := json.Unmarshal(a.raw, &folder); err != nil {
		t.Fatalf("not a folder: %s", a.raw)
	}
	return folder
}

// message is the sentence of a refusal, or of a one-sentence answer.
func (a answered) message(t *testing.T) string {
	t.Helper()
	var body struct {
		Message string `json:"message"`
		Error   struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(a.raw, &body); err != nil {
		t.Fatalf("not JSON: %s", a.raw)
	}
	return body.Message + body.Error.Message
}

// expect checks a status and, when one is given, the sentence beside it.
func (a answered) expect(t *testing.T, why string, status int, message string) {
	t.Helper()
	if a.status != status {
		t.Errorf("%s: answered %d, want %d: %s", why, a.status, status, a.raw)
		return
	}
	if message != "" && a.message(t) != message {
		t.Errorf("%s: said %q, want %q", why, a.message(t), message)
	}
}

// create makes a folder through the route and returns it.
func (s *site) create(userID, name string, parentID *int64) folderAnswer {
	s.t.Helper()
	body, _ := json.Marshal(map[string]any{"name": name, "parent_id": parentID})
	return s.ask(userID, http.MethodPost, "/api/v1/folders", string(body)).folder(s.t)
}

// names lists an account's folder names in the order the list answers.
func (s *site) names(userID string) []string {
	s.t.Helper()
	listed := s.ask(userID, http.MethodGet, "/api/v1/folders", "")
	var folders []folderAnswer
	if err := json.Unmarshal(listed.raw, &folders); err != nil || listed.status != http.StatusOK {
		s.t.Fatalf("the list answered %d: %s", listed.status, listed.raw)
	}
	names := make([]string, len(folders))
	for index, folder := range folders {
		names[index] = folder.Name
	}
	return names
}

func folderPathOf(id int64, suffix string) string {
	return fmt.Sprintf("/api/v1/folders/%d%s", id, suffix)
}

func pointer[T any](value T) *T { return &value }

// routeCases is every route's behaviour, asked of whatever rows newWorld
// makes. Each case makes its own accounts, so the cases do not see each
// other's folders even when they share a database.
func routeCases(t *testing.T, newWorld func(*testing.T) world) {
	t.Run("a folder is created, read, listed and traced to the top", func(t *testing.T) {
		s := newSite(t, newWorld(t))
		owner := s.world.account(t)

		// A body that still names a workspace is not refused; the field is
		// not read, and the folder is the caller's.
		made := s.ask(owner, http.MethodPost, "/api/v1/folders", `{"name": "Clinic", "workspace_id": 7}`)
		clinic := made.folder(t)
		if clinic.Name != "Clinic" || clinic.ParentID != nil || clinic.Position == "" || clinic.Metadata != nil {
			t.Errorf("created %+v", clinic)
		}
		if clinic.CreatedAt != clinic.UpdatedAt || !strings.HasPrefix(clinic.CreatedAt, "2026-03-04T05:06:08") {
			t.Errorf("created_at %q and updated_at %q should be the one reading of the clock", clinic.CreatedAt, clinic.UpdatedAt)
		}
		// The keys a client reads, in order, and no owner among them.
		var keys []string
		decoder := json.NewDecoder(strings.NewReader(string(made.raw)))
		decoder.Token()
		for decoder.More() {
			key, _ := decoder.Token()
			keys = append(keys, key.(string))
			var skipped json.RawMessage
			decoder.Decode(&skipped)
		}
		if want := []string{"id", "name", "position", "parent_id", "created_at", "updated_at", "metadata"}; !reflect.DeepEqual(keys, want) {
			t.Errorf("a folder's keys are %v, want %v", keys, want)
		}

		wards := s.create(owner, "Wards", &clinic.ID)
		second := s.create(owner, "Pharmacy", nil)
		if !(second.Position > clinic.Position) {
			t.Errorf("a new folder goes after its last sibling: %q then %q", clinic.Position, second.Position)
		}

		if got := s.ask(owner, http.MethodGet, folderPathOf(wards.ID, ""), "").folder(t); !reflect.DeepEqual(got, wards) {
			t.Errorf("read %+v, created %+v", got, wards)
		}
		crumbs := s.ask(owner, http.MethodGet, folderPathOf(wards.ID, "/breadcrumb"), "")
		if want := fmt.Sprintf(`[{"id":%d,"name":"Clinic"},{"id":%d,"name":"Wards"}]`, clinic.ID, wards.ID); string(crumbs.raw) != want {
			t.Errorf("breadcrumb %s, want %s", crumbs.raw, want)
		}
		// Position, then id: Clinic and Wards hold the same first key.
		if got, want := s.names(owner), []string{"Clinic", "Wards", "Pharmacy"}; !reflect.DeepEqual(got, want) {
			t.Errorf("listed %v, want %v", got, want)
		}
	})

	t.Run("what a create refuses", func(t *testing.T) {
		s := newSite(t, newWorld(t))
		owner := s.world.account(t)
		clinic := s.create(owner, "Clinic", nil)

		for _, each := range []struct {
			why, body string
			status    int
			message   string
		}{
			{"no name", `{}`, 422, ""},
			{"an empty name", `{"name": ""}`, 422, ""},
			{"a name longer than the column", `{"name": "` + strings.Repeat("n", MaxNameLength+1) + `"}`, 422, ""},
			{"a parent that is not a number", `{"name": "x", "parent_id": "abc"}`, 422, ""},
			{"a body that is not an object", `[]`, 422, ""},
			{"a parent nobody has", `{"name": "x", "parent_id": 999999999}`, 404, ParentNotFound},
			{"a parent no column can hold", `{"name": "x", "parent_id": 99999999999999999999999}`, 404, ParentNotFound},
			{"a name its sibling has", `{"name": "Clinic"}`, 409, NameTakenHere},
		} {
			s.ask(owner, http.MethodPost, "/api/v1/folders", each.body).expect(t, each.why, each.status, each.message)
		}
		// The same name under another parent is another folder.
		s.create(owner, "Clinic", &clinic.ID)

		// Eight levels are allowed and the ninth is not.
		parent := s.create(owner, "level 1", nil)
		for level := 2; level <= MaxDepth; level++ {
			parent = s.create(owner, fmt.Sprintf("level %d", level), &parent.ID)
		}
		body, _ := json.Marshal(map[string]any{"name": "too deep", "parent_id": parent.ID})
		s.ask(owner, http.MethodPost, "/api/v1/folders", string(body)).expect(t, "a ninth level", 400, tooDeep(MaxDepth+1))
	})

	t.Run("a path that is not a folder id", func(t *testing.T) {
		s := newSite(t, newWorld(t))
		owner := s.world.account(t)
		s.ask(owner, http.MethodGet, "/api/v1/folders/abc", "").expect(t, "letters", 422, "")
		s.ask(owner, http.MethodGet, "/api/v1/folders/99999999999999999999999", "").expect(t, "too wide for any id", 404, NotFound)
		s.ask(owner, http.MethodGet, "/api/v1/folders/999999999", "").expect(t, "nobody's", 404, NotFound)
		// Both the path and the body wrong: both are reported at once.
		both := s.ask(owner, http.MethodPut, "/api/v1/folders/abc", `{}`)
		both.expect(t, "a bad path and a bad body", 422, "")
		if !strings.Contains(string(both.raw), "folder_id") || !strings.Contains(string(both.raw), "name") {
			t.Errorf("the 422 names both failures: %s", both.raw)
		}
	})

	t.Run("a rename", func(t *testing.T) {
		s := newSite(t, newWorld(t))
		owner := s.world.account(t)
		clinic := s.create(owner, "Clinic", nil)
		s.create(owner, "Pharmacy", nil)

		renamed := s.ask(owner, http.MethodPut, folderPathOf(clinic.ID, ""), `{"name": "Outpatients"}`).folder(t)
		if renamed.Name != "Outpatients" || renamed.UpdatedAt == clinic.UpdatedAt || renamed.CreatedAt != clinic.CreatedAt {
			t.Errorf("renamed to %+v from %+v", renamed, clinic)
		}
		// The name it already has changes nothing, the time included.
		again := s.ask(owner, http.MethodPut, folderPathOf(clinic.ID, ""), `{"name": "Outpatients"}`).folder(t)
		if !reflect.DeepEqual(again, renamed) {
			t.Errorf("a rename to the same name changed %+v to %+v", renamed, again)
		}
		s.ask(owner, http.MethodPut, folderPathOf(clinic.ID, ""), `{"name": "Pharmacy"}`).expect(t, "a sibling's name", 409, NameTakenHere)
		s.ask(owner, http.MethodPut, folderPathOf(clinic.ID, ""), `{"name": ""}`).expect(t, "an empty name", 422, "")
		if stored := s.world.folderRow(t, clinic.ID); stored.Name != "Outpatients" {
			t.Errorf("the row is named %q", stored.Name)
		}
	})

	t.Run("a move under another parent", func(t *testing.T) {
		s := newSite(t, newWorld(t))
		owner := s.world.account(t)
		clinic := s.create(owner, "Clinic", nil)
		wards := s.create(owner, "Wards", &clinic.ID)
		beds := s.create(owner, "Beds", &wards.ID)
		archive := s.create(owner, "Archive", nil)
		existing := s.create(owner, "Old", &archive.ID)
		s.create(owner, "Wards", &archive.ID)

		move := func(id int64, to any) answered {
			body, _ := json.Marshal(map[string]any{"new_parent_id": to})
			return s.ask(owner, http.MethodPut, folderPathOf(id, "/move"), string(body))
		}
		move(clinic.ID, clinic.ID).expect(t, "into itself", 400, MovedIntoItself)
		move(clinic.ID, beds.ID).expect(t, "into its own subtree", 400, MovedIntoOwnSubree)
		move(clinic.ID, 999999999).expect(t, "into nobody's folder", 404, TargetNotFound)
		move(wards.ID, archive.ID).expect(t, "where a sibling has its name", 409, NameTakenAtTarget)
		move(clinic.ID, "abc").expect(t, "a target that is not a number", 422, "")

		moved := move(beds.ID, archive.ID).folder(t)
		if moved.ParentID == nil || *moved.ParentID != archive.ID || !(moved.Position > existing.Position) || moved.UpdatedAt == beds.UpdatedAt {
			t.Errorf("moved to %+v; the last sibling there is at %q", moved, existing.Position)
		}
		// Where it already is: nothing changes, and it does not jump to
		// the end of its own siblings.
		if still := move(beds.ID, archive.ID).folder(t); !reflect.DeepEqual(still, moved) {
			t.Errorf("a move to the same parent changed %+v to %+v", moved, still)
		}
		top := move(beds.ID, nil).folder(t)
		if top.ParentID != nil {
			t.Errorf("a move to the top level left the parent %v", *top.ParentID)
		}

		// A subtree may not be pushed past the depth limit: Clinic has one
		// level below it, so it fits under level 6 and not under level 7.
		parent := s.create(owner, "level 1", nil)
		var sixth folderAnswer
		for level := 2; level <= MaxDepth-1; level++ {
			parent = s.create(owner, fmt.Sprintf("level %d", level), &parent.ID)
			if level == MaxDepth-2 {
				sixth = parent
			}
		}
		move(clinic.ID, parent.ID).expect(t, "a subtree that would reach level nine", 400, tooDeep(MaxDepth+1))
		move(clinic.ID, sixth.ID).expect(t, "a subtree that reaches level eight", 200, "")
	})

	t.Run("a reorder among siblings", func(t *testing.T) {
		s := newSite(t, newWorld(t))
		owner := s.world.account(t)
		first := s.create(owner, "First", nil)
		second := s.create(owner, "Second", nil)
		third := s.create(owner, "Third", nil)

		body, _ := json.Marshal(map[string]any{"before_position": first.Position, "after_position": second.Position})
		between := s.ask(owner, http.MethodPut, folderPathOf(third.ID, "/reorder"), string(body)).folder(t)
		if !(between.Position > first.Position && between.Position < second.Position) {
			t.Errorf("%q is not between %q and %q", between.Position, first.Position, second.Position)
		}
		if got, want := s.names(owner), []string{"First", "Third", "Second"}; !reflect.DeepEqual(got, want) {
			t.Errorf("listed %v, want %v", got, want)
		}
		// With neither neighbour named it goes to the end.
		last := s.ask(owner, http.MethodPut, folderPathOf(first.ID, "/reorder"), `{}`).folder(t)
		if !(last.Position > second.Position) {
			t.Errorf("%q is not after the last sibling's %q", last.Position, second.Position)
		}
		for why, body := range map[string]string{
			"a made-up position":         `{"before_position": "!!"}`,
			"an empty position":          `{"after_position": ""}`,
			"the two in the wrong order": fmt.Sprintf(`{"before_position": %q, "after_position": %q}`, second.Position, between.Position),
		} {
			s.ask(owner, http.MethodPut, folderPathOf(third.ID, "/reorder"), body).expect(t, why, 400, InvalidPosition)
		}
		s.ask(owner, http.MethodPut, folderPathOf(third.ID, "/reorder"), `{"before_position": 3}`).expect(t, "a position that is not text", 422, "")
	})

	t.Run("a watched folder stops being watched, and only its flag changes", func(t *testing.T) {
		s := newSite(t, newWorld(t))
		owner := s.world.account(t)
		watched := s.create(owner, "Synced", nil)
		plain := s.create(owner, "Plain", nil)
		s.world.setMetadata(t, watched.ID, `{"watched": true, "parser": "local", "ratio": 0.25}`)
		s.world.setMetadata(t, plain.ID, `{"colour": "green"}`)

		s.ask(owner, http.MethodPatch, folderPathOf(watched.ID, "/watched"), "").expect(t, "a watched folder", 200, WatchUpdated)
		after := s.ask(owner, http.MethodGet, folderPathOf(watched.ID, ""), "").folder(t)
		if want := map[string]any{"watched": false, "parser": "local", "ratio": 0.25}; !reflect.DeepEqual(after.Metadata, want) {
			t.Errorf("metadata is %v, want %v", after.Metadata, want)
		}
		if after.UpdatedAt == watched.UpdatedAt {
			t.Error("turning the flag off did not move updated_at")
		}
		// Asking again, or of a folder never watched, writes nothing.
		s.ask(owner, http.MethodPatch, folderPathOf(watched.ID, "/watched"), "").expect(t, "asked twice", 200, WatchUpdated)
		if again := s.ask(owner, http.MethodGet, folderPathOf(watched.ID, ""), "").folder(t); !reflect.DeepEqual(again, after) {
			t.Errorf("asking twice changed %+v to %+v", after, again)
		}
		s.ask(owner, http.MethodPatch, folderPathOf(plain.ID, "/watched"), "").expect(t, "a folder never watched", 200, WatchUpdated)
		untouched := s.ask(owner, http.MethodGet, folderPathOf(plain.ID, ""), "").folder(t)
		if want := map[string]any{"colour": "green"}; !reflect.DeepEqual(untouched.Metadata, want) || untouched.UpdatedAt != plain.UpdatedAt {
			t.Errorf("a folder never watched was changed: %+v", untouched)
		}
	})

	t.Run("metadata that is not an object is answered as none", func(t *testing.T) {
		s := newSite(t, newWorld(t))
		owner := s.world.account(t)
		odd := s.create(owner, "Odd", nil)
		s.create(owner, "Fine", nil)
		s.world.setMetadata(t, odd.ID, `["not", "an", "object"]`)
		if got := s.ask(owner, http.MethodGet, folderPathOf(odd.ID, ""), "").folder(t); got.Metadata != nil {
			t.Errorf("metadata is %v", got.Metadata)
		}
		// One odd row does not take the list down.
		if got, want := s.names(owner), []string{"Odd", "Fine"}; !reflect.DeepEqual(got, want) {
			t.Errorf("listed %v, want %v", got, want)
		}
	})

	t.Run("a delete marks the subtree's documents and queues their removal", func(t *testing.T) {
		s := newSite(t, newWorld(t))
		owner := s.world.account(t)
		clinic := s.create(owner, "Clinic", nil)
		wards := s.create(owner, "Wards", &clinic.ID)
		other := s.create(owner, "Other", nil)
		inClinic := s.world.addDocument(t, owner, &clinic.ID, "ready")
		inWards := s.world.addDocument(t, owner, &wards.ID, "processing")
		going := s.world.addDocument(t, owner, &wards.ID, "deleting")
		outside := s.world.addDocument(t, owner, &other.ID, "ready")
		loose := s.world.addDocument(t, owner, nil, "ready")

		answer := s.ask(owner, http.MethodDelete, folderPathOf(clinic.ID, ""), "")
		if want := `{"message":"Folder deletion started","documents_queued_for_deletion":2}`; answer.status != 200 || string(answer.raw) != want {
			t.Fatalf("answered %d %s, want %s", answer.status, answer.raw, want)
		}
		for id, want := range map[int64]string{inClinic: "deleting", inWards: "deleting", going: "deleting", outside: "ready", loose: "ready"} {
			if _, state, _ := s.world.document(t, id); state != want {
				t.Errorf("document %d is %s, want %s", id, state, want)
			}
		}
		if len(s.queue.messages) != 1 {
			t.Fatalf("%d messages were queued", len(s.queue.messages))
		}
		message := s.queue.messages[0]
		// The documents the marker was written on, and the folders to
		// remove after them, the folder itself first. The document that was
		// already being deleted is another job's.
		if message.Task != TaskName ||
			!reflect.DeepEqual(message.Args, []any{[]any{inClinic, inWards}}) ||
			!reflect.DeepEqual(message.Kwargs, map[string]any{"folder_subtree_ids": []any{clinic.ID, wards.ID}, "user_id": owner}) {
			t.Errorf("queued %s %v %v", message.Task, message.Args, message.Kwargs)
		}
		if message.Context.UserID != owner {
			t.Errorf("the job's log context names %q, want the caller", message.Context.UserID)
		}

		// An empty folder still queues, because the job is what deletes it.
		s.ask(owner, http.MethodDelete, folderPathOf(other.ID, ""), "").expect(t, "a folder with no folders under it", 200, DeletionStarted)
		if len(s.queue.messages) != 2 || !reflect.DeepEqual(s.queue.messages[1].Args, []any{[]any{outside}}) {
			t.Errorf("the second delete queued %+v", s.queue.messages[1:])
		}
		empty := s.create(owner, "Empty", nil)
		s.ask(owner, http.MethodDelete, folderPathOf(empty.ID, ""), "").expect(t, "an empty folder", 200, DeletionStarted)
		if len(s.queue.messages) != 3 || !reflect.DeepEqual(s.queue.messages[2].Args, []any{[]any{}}) ||
			!reflect.DeepEqual(s.queue.messages[2].Kwargs, map[string]any{"folder_subtree_ids": []any{empty.ID}, "user_id": owner}) {
			t.Errorf("the empty folder's delete queued %+v", s.queue.messages[2:])
		}
	})

	t.Run("a delete that cannot be queued puts every document back as it was", func(t *testing.T) {
		s := newSite(t, newWorld(t))
		owner := s.world.account(t)
		clinic := s.create(owner, "Clinic", nil)
		ready := s.world.addDocument(t, owner, &clinic.ID, "ready")
		failed := s.world.addDocument(t, owner, &clinic.ID, "failed")
		s.queue.down = true

		s.ask(owner, http.MethodDelete, folderPathOf(clinic.ID, ""), "").expect(t, "the queue is down", 503, QueueFailed)
		// Each gets its own status back, not "ready" for all.
		for id, want := range map[int64]string{ready: "ready", failed: "failed"} {
			if _, state, _ := s.world.document(t, id); state != want {
				t.Errorf("document %d was left %s, want %s", id, state, want)
			}
		}
	})

	t.Run("a document is filed, and a ready one arriving is announced", func(t *testing.T) {
		s := newSite(t, newWorld(t))
		owner := s.world.account(t)
		clinic := s.create(owner, "Clinic", nil)
		wards := s.create(owner, "Wards", nil)
		ready := s.world.addDocument(t, owner, nil, "ready")
		processing := s.world.addDocument(t, owner, nil, "processing")

		file := func(document int64, folder any) answered {
			body, _ := json.Marshal(map[string]any{"folder_id": folder})
			return s.ask(owner, http.MethodPut, fmt.Sprintf("/api/v1/documents/%d/move", document), string(body))
		}
		in := func(document int64) *int64 {
			folderID, _, _ := s.world.document(t, document)
			return folderID
		}

		file(ready, clinic.ID).expect(t, "into a folder", 200, DocumentMoved)
		file(ready, clinic.ID).expect(t, "into the folder it is in", 200, DocumentMoved)
		file(ready, wards.ID).expect(t, "into another folder", 200, DocumentMoved)
		file(processing, clinic.ID).expect(t, "a document not ready", 200, DocumentMoved)
		if got := in(processing); got == nil || *got != clinic.ID {
			t.Errorf("the processing document is in %v", got)
		}
		file(ready, nil).expect(t, "out of every folder", 200, DocumentMoved)
		if got := in(ready); got != nil {
			t.Errorf("the document is still in folder %d", *got)
		}
		// Two arrivals: into Clinic, then from Clinic into Wards. Not the
		// move to where it already was, not the one that was not ready, not
		// the move out.
		want := []Arrival{
			{UserID: owner, DocumentID: ready, FolderID: clinic.ID, DocumentType: "FILE", Title: "Document"},
			{UserID: owner, DocumentID: ready, FolderID: wards.ID, PreviousFolderID: &clinic.ID, DocumentType: "FILE", Title: "Document"},
		}
		if !reflect.DeepEqual(s.told.arrivals, want) {
			t.Errorf("announced %+v, want %+v", s.told.arrivals, want)
		}

		file(ready, 999999999).expect(t, "into nobody's folder", 404, TargetFolderNotFound)
		file(999999999, clinic.ID).expect(t, "nobody's document", 404, DocumentNotFound)
		file(ready, "abc").expect(t, "a folder that is not a number", 422, "")
		s.ask(owner, http.MethodPut, "/api/v1/documents/abc/move", `{"folder_id": null}`).expect(t, "a document id that is not a number", 422, "")
	})

	t.Run("several documents are filed at once", func(t *testing.T) {
		s := newSite(t, newWorld(t))
		owner := s.world.account(t)
		clinic := s.create(owner, "Clinic", nil)
		one := s.world.addDocument(t, owner, nil, "ready")
		two := s.world.addDocument(t, owner, &clinic.ID, "ready")
		three := s.world.addDocument(t, owner, nil, "failed")

		bulk := func(body string) answered {
			return s.ask(owner, http.MethodPut, "/api/v1/documents/bulk-move", body)
		}
		// Asked out of order and with an id nobody has: three are found,
		// and three is the count.
		body := fmt.Sprintf(`{"document_ids": [%d, %d, 999999999, %d], "folder_id": %d}`, three, one, two, clinic.ID)
		bulk(body).expect(t, "a selection", 200, "3 documents moved successfully")
		for _, id := range []int64{one, two, three} {
			if folderID, _, _ := s.world.document(t, id); folderID == nil || *folderID != clinic.ID {
				t.Errorf("document %d is in %v", id, folderID)
			}
		}
		// Only the ready document that actually arrived is announced.
		if want := []Arrival{{UserID: owner, DocumentID: one, FolderID: clinic.ID, DocumentType: "FILE", Title: "Document"}}; !reflect.DeepEqual(s.told.arrivals, want) {
			t.Errorf("announced %+v, want %+v", s.told.arrivals, want)
		}

		bulk(`{"document_ids": [], "folder_id": null}`).expect(t, "an empty selection", 400, NoDocumentIDs)
		bulk(`{"document_ids": [999999999]}`).expect(t, "nothing found", 404, NoDocumentsFound)
		bulk(fmt.Sprintf(`{"document_ids": [%d], "folder_id": 999999999}`, one)).expect(t, "into nobody's folder", 404, TargetFolderNotFound)
		bulk(`{"folder_id": null}`).expect(t, "no list", 422, "")
		bulk(`{"document_ids": ["x"]}`).expect(t, "an id that is not a number", 422, "")
	})

	t.Run("another account's folders and documents do not exist", func(t *testing.T) {
		s := newSite(t, newWorld(t))
		a, b := s.world.account(t), s.world.account(t)
		clinic := s.create(a, "Clinic", nil)
		wards := s.create(a, "Wards", &clinic.ID)
		s.world.setMetadata(t, clinic.ID, `{"watched": true}`)
		filed := s.world.addDocument(t, a, &clinic.ID, "ready")
		loose := s.world.addDocument(t, a, nil, "ready")
		own := s.create(b, "Mine", nil)
		ownDocument := s.world.addDocument(t, b, nil, "ready")
		before := *s.world.folderRow(t, clinic.ID)

		// Every route that names a folder, on every method, asked by B of
		// A's folder: the 404 a folder nobody has would get.
		for _, each := range []struct{ verb, suffix, body string }{
			{http.MethodGet, "", ""},
			{http.MethodGet, "/breadcrumb", ""},
			{http.MethodPut, "", `{"name": "Taken over"}`},
			{http.MethodPut, "/move", `{"new_parent_id": null}`},
			{http.MethodPut, "/move", fmt.Sprintf(`{"new_parent_id": %d}`, own.ID)},
			{http.MethodPut, "/reorder", `{}`},
			{http.MethodPatch, "/watched", ""},
			{http.MethodDelete, "", ""},
		} {
			for _, folder := range []int64{clinic.ID, wards.ID} {
				theirs := s.ask(b, each.verb, folderPathOf(folder, each.suffix), each.body)
				nobodys := s.ask(b, each.verb, folderPathOf(999999999, each.suffix), each.body)
				theirs.expect(t, each.verb+" "+each.suffix, 404, NotFound)
				if theirs.message(t) != nobodys.message(t) || theirs.status != nobodys.status {
					t.Errorf("%s %s: A's folder answers %d %q and nobody's %d %q", each.verb, each.suffix,
						theirs.status, theirs.message(t), nobodys.status, nobodys.message(t))
				}
			}
		}

		// B's list has B's folder and nothing of A's; A's is whole.
		if got, want := s.names(b), []string{"Mine"}; !reflect.DeepEqual(got, want) {
			t.Errorf("B lists %v, want %v", got, want)
		}

		// B cannot build on A's tree: not create under it, not move into it.
		body, _ := json.Marshal(map[string]any{"name": "Planted", "parent_id": clinic.ID})
		s.ask(b, http.MethodPost, "/api/v1/folders", string(body)).expect(t, "create under A's folder", 404, ParentNotFound)
		body, _ = json.Marshal(map[string]any{"new_parent_id": clinic.ID})
		s.ask(b, http.MethodPut, folderPathOf(own.ID, "/move"), string(body)).expect(t, "move into A's folder", 404, TargetNotFound)

		// B cannot move A's documents, nor file its own in A's folder.
		for _, document := range []int64{filed, loose} {
			s.ask(b, http.MethodPut, fmt.Sprintf("/api/v1/documents/%d/move", document), `{"folder_id": null}`).
				expect(t, "move A's document out", 404, DocumentNotFound)
			s.ask(b, http.MethodPut, fmt.Sprintf("/api/v1/documents/%d/move", document), fmt.Sprintf(`{"folder_id": %d}`, own.ID)).
				expect(t, "move A's document into B's folder", 404, DocumentNotFound)
		}
		s.ask(b, http.MethodPut, fmt.Sprintf("/api/v1/documents/%d/move", ownDocument), fmt.Sprintf(`{"folder_id": %d}`, clinic.ID)).
			expect(t, "file B's document in A's folder", 404, TargetFolderNotFound)
		s.ask(b, http.MethodPut, "/api/v1/documents/bulk-move", fmt.Sprintf(`{"document_ids": [%d, %d], "folder_id": null}`, filed, loose)).
			expect(t, "bulk-move only A's documents", 404, NoDocumentsFound)
		s.ask(b, http.MethodPut, "/api/v1/documents/bulk-move", fmt.Sprintf(`{"document_ids": [%d], "folder_id": %d}`, ownDocument, clinic.ID)).
			expect(t, "bulk-move into A's folder", 404, TargetFolderNotFound)
		// A selection mixing both accounts' documents moves B's alone, and
		// counts B's alone.
		s.ask(b, http.MethodPut, "/api/v1/documents/bulk-move",
			fmt.Sprintf(`{"document_ids": [%d, %d, %d], "folder_id": %d}`, filed, loose, ownDocument, own.ID)).
			expect(t, "a mixed selection", 200, "1 documents moved successfully")
		if folderID, _, _ := s.world.document(t, ownDocument); folderID == nil || *folderID != own.ID {
			t.Errorf("B's own document was not filed: %v", folderID)
		}

		// Nothing of A's changed, nothing was queued, and the only arrival
		// announced is B's own.
		if after := *s.world.folderRow(t, clinic.ID); !reflect.DeepEqual(after, before) {
			t.Errorf("A's folder changed from %+v to %+v", before, after)
		}
		if got, want := s.names(a), []string{"Clinic", "Wards"}; !reflect.DeepEqual(got, want) {
			t.Errorf("A lists %v, want %v", got, want)
		}
		for id, want := range map[int64]*int64{filed: &clinic.ID, loose: nil} {
			folderID, state, exists := s.world.document(t, id)
			if !exists || state != "ready" || !sameFolder(folderID, want) {
				t.Errorf("A's document %d: exists=%v state=%s folder=%v", id, exists, state, folderID)
			}
		}
		if len(s.queue.messages) != 0 {
			t.Errorf("B's requests queued %d jobs", len(s.queue.messages))
		}
		if len(s.told.arrivals) != 1 || s.told.arrivals[0].UserID != b || s.told.arrivals[0].DocumentID != ownDocument {
			t.Errorf("announced %+v", s.told.arrivals)
		}

		// And the two accounts may each have a top-level folder of one
		// name: a name is taken among one account's siblings only.
		s.create(b, "Clinic", nil)
	})
}

// Whether a move is an arrival, one condition at a time. Neither move's
// answer says which happened, so a mistake here would be an automation
// that quietly stops firing.
func TestWhichMovesAreArrivals(t *testing.T) {
	ready := Document{ID: 1, Title: "t", DocumentType: "NOTE", Status: []byte(`{"state": "ready"}`)}
	in := func(document Document, folder int64) Document { document.FolderID = &folder; return document }
	status := func(document Document, status string) Document { document.Status = []byte(status); return document }

	for _, each := range []struct {
		why      string
		document Document
		target   *int64
		owed     bool
	}{
		{"a ready document into a folder", ready, pointer(int64(7)), true},
		{"from one folder to another", in(ready, 7), pointer(int64(8)), true},
		{"out of every folder: it arrived nowhere", in(ready, 7), nil, false},
		{"into the folder it is already in", in(ready, 7), pointer(int64(7)), false},
		{"in no folder, moved to none", ready, nil, false},
		{"still processing", status(ready, `{"state": "processing"}`), pointer(int64(7)), false},
		{"being deleted", status(ready, `{"state": "deleting"}`), pointer(int64(7)), false},
		{"a status with no state", status(ready, `{}`), pointer(int64(7)), false},
		{"a status that is not an object", status(ready, `"ready"`), pointer(int64(7)), false},
		{"no status at all", status(ready, ``), pointer(int64(7)), false},
	} {
		arrival, owed := arrivalOf("someone", each.document, each.target)
		if owed != each.owed {
			t.Errorf("%s: an arrival=%v, want %v", each.why, owed, each.owed)
		}
		if owed && (arrival.UserID != "someone" || arrival.FolderID != *each.target ||
			!sameFolder(arrival.PreviousFolderID, each.document.FolderID)) {
			t.Errorf("%s: announced %+v", each.why, arrival)
		}
	}
}

// An announcement that fails does not fail the move: the document is where
// the person put it.
func TestAMoveStandsWhenItsArrivalCannotBeAnnounced(t *testing.T) {
	w := newMemory()
	s := newSite(t, w)
	s.told.failing = true
	owner := w.account(t)
	clinic := s.create(owner, "Clinic", nil)
	document := w.addDocument(t, owner, nil, "ready")

	s.ask(owner, http.MethodPut, fmt.Sprintf("/api/v1/documents/%d/move", document),
		fmt.Sprintf(`{"folder_id": %d}`, clinic.ID)).expect(t, "the move", 200, DocumentMoved)
	if folderID, _, _ := w.document(t, document); folderID == nil || *folderID != clinic.ID {
		t.Errorf("the document is in %v", folderID)
	}
}
