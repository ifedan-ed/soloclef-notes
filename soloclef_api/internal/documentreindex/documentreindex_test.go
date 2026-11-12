package documentreindex

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice/parserchoicetest"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

// The two accounts: the caller, and somebody else.
const (
	owner    = "11111111-2222-4333-8444-555555555555"
	stranger = "99999999-2222-4333-8444-555555555555"
)

// shelf is the documents, the queue and what happened, for one test.
type shelf struct {
	documents map[int64]*Subject
	// owners is whose each document is; one that is not named is the
	// caller's.
	owners  map[int64]string
	parsers map[int64]string
	// raced makes the write find the document already taken by another
	// request.
	raced     bool
	queueDown bool
	events    []string
}

// owns is the predicate both statements carry: the id and the owner.
func (s *shelf) owns(documentID int64, userID string) bool {
	whose, named := s.owners[documentID]
	if !named {
		whose = owner
	}
	_, found := s.documents[documentID]
	return found && whose == userID
}

func (s *shelf) Subject(_ context.Context, documentID int64, userID string) (*Subject, error) {
	if !s.owns(documentID, userID) {
		return nil, nil
	}
	held := s.documents[documentID]
	copied := *held
	return &copied, nil
}

func (s *shelf) HandBack(_ context.Context, documentID int64, userID, parser string) (bool, error) {
	if s.raced || !s.owns(documentID, userID) {
		return false, nil
	}
	s.documents[documentID].State = "pending"
	if parser != "" {
		s.parsers[documentID] = parser
	}
	s.events = append(s.events, "pending")
	return true, nil
}

func (s *shelf) Enqueue(_ context.Context, documentID int64, _ produce.LogContext) error {
	if s.queueDown {
		s.documents[documentID].State = "failed"
		return errors.New("the broker did not answer")
	}
	s.events = append(s.events, "queued")
	return nil
}

func newShelf() *shelf {
	return &shelf{
		parsers: map[int64]string{1: "local"},
		owners:  map[int64]string{8: stranger},
		documents: map[int64]*Subject{
			1: {State: "failed", Filename: "thesis.pdf"},
			2: {State: "ready", Filename: "deck.pptx"},
			3: {State: "processing", Filename: "thesis.pdf"},
			4: {State: "deleting", Filename: "thesis.pdf"},
			5: {State: "ready", HasText: true}, // a note
			6: {State: "failed"},               // nothing to read
			8: {State: "ready", Filename: "a.pdf"},
			9: {State: "pending", Filename: "a.pdf"},
		},
	}
}

func ask(t *testing.T, held *shelf, target string) *httptest.ResponseRecorder {
	t.Helper()
	return askAs(t, held, owner, target)
}

func askAs(t *testing.T, held *shelf, caller, target string) *httptest.ResponseRecorder {
	t.Helper()
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	Route{Rows: held, Indexing: held,
		// Local and LlamaParse are configured; Datalab and Unstructured
		// have no key.
		Parsers: parserchoicetest.Registry(t, parse.Local, parse.LlamaParse),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil))}.Register(api)
	request := httptest.NewRequest(http.MethodPost, target, nil)
	request = request.WithContext(principal.WithPrincipal(request.Context(),
		principal.Principal{UserID: caller, Method: principal.MethodSession}))
	recorder := httptest.NewRecorder()
	api.Adapter().ServeHTTP(recorder, request)
	return recorder
}

func TestAReindexSetsTheDocumentWaitingThenQueuesIt(t *testing.T) {
	held := newShelf()
	answer := ask(t, held, "/api/v1/documents/1/reindex")
	if answer.Code != http.StatusOK || !strings.Contains(answer.Body.String(), `"message":"Document queued for indexing","document_id":1`) {
		t.Fatalf("answered %d %s", answer.Code, answer.Body)
	}
	// The row first, the message after: the worker reads the row.
	if got := strings.Join(held.events, ","); got != "pending,queued" {
		t.Errorf("order %q", got)
	}
	// No parser was named, so the document keeps its own.
	if held.parsers[1] != "local" {
		t.Errorf("parser became %q", held.parsers[1])
	}
}

func TestAReindexCanMoveADocumentToAnotherParser(t *testing.T) {
	held := newShelf()
	// deck.pptx is read by LlamaParse and not by the local parser.
	answer := ask(t, held, "/api/v1/documents/2/reindex?parser=llamaparse")
	if answer.Code != http.StatusOK {
		t.Fatalf("answered %d %s", answer.Code, answer.Body)
	}
	if held.parsers[2] != "llamaparse" {
		t.Errorf("recorded parser %q", held.parsers[2])
	}
}

func TestWhatAReindexIsRefusedFor(t *testing.T) {
	for _, test := range []struct {
		name, target string
		status       int
		says         string
	}{
		{"no such document", "/api/v1/documents/404/reindex", 404, "Document with id 404 not found"},
		{"an id that is not a number", "/api/v1/documents/x/reindex", 422, ""},
		{"another account's document looks like no document", "/api/v1/documents/8/reindex", 404, "Document with id 8 not found"},
		{"a document already being indexed", "/api/v1/documents/3/reindex", 409, AlreadyQueued},
		{"a document already waiting", "/api/v1/documents/9/reindex", 409, AlreadyQueued},
		{"a document being deleted", "/api/v1/documents/4/reindex", 409, BeingDeleted},
		{"a document with neither a file nor text", "/api/v1/documents/6/reindex", 409, NothingToRead},
		{"a parser for a document that has no file", "/api/v1/documents/5/reindex?parser=local", 400, NoFileToParse},
		{"a parser with no key, by name", "/api/v1/documents/1/reindex?parser=datalab", 400, "The datalab parser is not configured on this server"},
		{"a word that is not a parser", "/api/v1/documents/1/reindex?parser=cloud", 400, `\"cloud\" is not a parser`},
		{"a parser that does not read the document's file", "/api/v1/documents/2/reindex?parser=local", 400, "cannot be read by the local parser"},
	} {
		t.Run(test.name, func(t *testing.T) {
			held := newShelf()
			answer := ask(t, held, test.target)
			if answer.Code != test.status || !strings.Contains(answer.Body.String(), test.says) {
				t.Fatalf("answered %d %s, want %d saying %q", answer.Code, answer.Body, test.status, test.says)
			}
			if len(held.events) != 0 {
				t.Errorf("a refused re-index did %v", held.events)
			}
		})
	}
}

func TestANoteIsReindexedFromItsOwnText(t *testing.T) {
	held := newShelf()
	if answer := ask(t, held, "/api/v1/documents/5/reindex"); answer.Code != http.StatusOK {
		t.Fatalf("answered %d %s", answer.Code, answer.Body)
	}
}

func TestTwoReindexesOfOneDocumentQueueOneJob(t *testing.T) {
	held := newShelf()
	held.raced = true
	answer := ask(t, held, "/api/v1/documents/1/reindex")
	if answer.Code != http.StatusConflict || !strings.Contains(answer.Body.String(), AlreadyQueued) {
		t.Fatalf("answered %d %s", answer.Code, answer.Body)
	}
	if len(held.events) != 0 {
		t.Errorf("the loser of the race did %v", held.events)
	}
}

func TestAReindexThatCannotBeQueuedSaysSoAndDoesNotLeaveItWaiting(t *testing.T) {
	held := newShelf()
	held.queueDown = true
	answer := ask(t, held, "/api/v1/documents/1/reindex")
	if answer.Code != http.StatusServiceUnavailable || !strings.Contains(answer.Body.String(), NotQueued) {
		t.Fatalf("answered %d %s", answer.Code, answer.Body)
	}
	if state := held.documents[1].State; state != "failed" {
		t.Errorf("the document is left %q with no job behind it", state)
	}
}

// The isolation rule, from the other side: account B asks for each of
// account A's documents, in every state, with and without a parser. Each is
// answered as an id that names nothing, nothing is written and nothing is
// queued.
func TestAnotherAccountCannotReindexADocument(t *testing.T) {
	for id := range newShelf().documents {
		if id == 8 {
			continue // the stranger's own
		}
		for _, query := range []string{"", "?parser=llamaparse"} {
			held := newShelf()
			before := *held.documents[id]
			target := "/api/v1/documents/" + strconv.FormatInt(id, 10) + "/reindex" + query
			answer := askAs(t, held, stranger, target)
			// Word for word what TestWhatAReindexIsRefusedFor pins for an
			// id that names nothing.
			want := `"message":"Document with id ` + strconv.FormatInt(id, 10) + ` not found","status":404`
			if answer.Code != http.StatusNotFound || !strings.Contains(answer.Body.String(), want) {
				t.Errorf("%s as another account answered %d %s, want the 404 an unused id gets", target, answer.Code, answer.Body)
			}
			if len(held.events) != 0 || *held.documents[id] != before {
				t.Errorf("%s as another account did %v and left %+v", target, held.events, *held.documents[id])
			}
		}
	}
}
