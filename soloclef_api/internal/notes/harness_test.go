package notes

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// queued records the indexing jobs a test's requests asked for.
type queued struct {
	mu   sync.Mutex
	jobs []job
}

type job struct {
	documentID int64
	delay      time.Duration
}

func (q *queued) EnqueueAfter(_ context.Context, documentID int64, delay time.Duration, _ produce.LogContext) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.jobs = append(q.jobs, job{documentID, delay})
	return nil
}

// taken returns the jobs queued since it was last asked.
func (q *queued) taken() []job {
	q.mu.Lock()
	defer q.mu.Unlock()
	jobs := q.jobs
	q.jobs = nil
	return jobs
}

// rowRemover stands in for documentdeletion.Removal, which has tests of its
// own: it deletes the row, and the side row goes with it.
type rowRemover struct{ pool *pgxpool.Pool }

func (r rowRemover) Remove(ctx context.Context, documentID int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM documents WHERE id = $1`, documentID)
	return err
}

// server is the routes as cmd/api mounts them, the request's caller being
// whoever the test says: request id, the body reader, the caller, the
// routes. The gate that turns a credential into a caller is
// internal/principal's and is not under test here.
type server struct {
	t      *testing.T
	http   *httptest.Server
	caller string
	jobs   *queued
}

func serve(t *testing.T, pool *pgxpool.Pool) *server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	jobs := &queued{}
	s := &server{t: t, jobs: jobs}
	mux := http.NewServeMux()
	var remover Remover
	if pool != nil {
		remover = rowRemover{pool}
	}
	Routes{
		Pool: pool, Indexing: jobs, Removal: remover,
		Errors: errorenvelope.Writer{ReportURL: "https://notes.example/contact"},
		Logger: logger, IndexDelay: 3 * time.Second,
	}.Register(humago.New(mux, huma.DefaultConfig("test", "0.0.1")))
	var answer http.Handler = http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		ctx := principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: s.caller, Method: principal.MethodSession})
		mux.ServeHTTP(response, request.WithContext(ctx))
	})
	answer = requestbody.Middleware{
		Covers: requestbody.CoveringRoutes(WithBodies()...),
	}.Wrap(answer)
	s.http = httptest.NewServer(requestcontext.Middleware(logger, answer))
	t.Cleanup(s.http.Close)
	return s
}

// reply is an answer: its status and its body decoded, when it has one.
type reply struct {
	status int
	body   map[string]any
	raw    string
}

func (s *server) as(userID string) *server {
	s.caller = userID
	return s
}

func (s *server) send(verb, path, body string) reply {
	s.t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequest(verb, s.http.URL+path, reader)
	if err != nil {
		s.t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := s.http.Client().Do(request)
	if err != nil {
		s.t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	answer := reply{status: response.StatusCode, raw: string(raw)}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &answer.body)
	}
	return answer
}

// expect fails the test unless the answer has the status.
func (r reply) expect(t *testing.T, status int) reply {
	t.Helper()
	if r.status != status {
		t.Fatalf("status %d, want %d: %s", r.status, status, r.raw)
	}
	return r
}

// id is the answer's note id.
func (r reply) id() int64 {
	value, _ := r.body["id"].(float64)
	return int64(value)
}

// titles is the titles of a list's items, in order.
func (r reply) titles() []string {
	items, _ := r.body["items"].([]any)
	titles := make([]string, 0, len(items))
	for _, item := range items {
		titles = append(titles, item.(map[string]any)["title"].(string))
	}
	return titles
}
