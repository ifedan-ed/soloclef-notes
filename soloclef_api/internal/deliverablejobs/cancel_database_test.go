package deliverablejobs

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres. **Every account in it is
// deleted** (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_DELIVERABLEJOBS_DATABASE"

func TestAJobIsStoppedByItsOwnerAlone(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, pool, `
INSERT INTO deliverable_jobs (id, user_id, kind, title, tool_call_id, status) VALUES
 (1, '`+alice+`', 'report', 'Queued', 'a', 'queued'),
 (2, '`+alice+`', 'report', 'Running', 'b', 'running'),
 (3, '`+alice+`', 'report', 'Done', 'c', 'ready'),
 (4, '`+bob+`',   'report', 'His', 'd', 'queued');`)
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	CancelRoute{Database: pool, Errors: errorenvelope.Writer{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC) }}.Register(api)
	cancel := func(userID, id string) (int, string) {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/deliverable-jobs/"+id+"/cancel", nil)
		request = request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: userID, Method: principal.MethodSession}))
		recorder := httptest.NewRecorder()
		api.Adapter().ServeHTTP(recorder, request)
		return recorder.Code, recorder.Body.String()
	}
	for _, c := range []struct {
		name, caller, id string
		status           int
		says             string
	}{
		{"a queued job is cancelled at once", alice, "1", 200, `"status":"cancelled"`},
		{"asking again answers it as it is", alice, "1", 200, `"status":"cancelled"`},
		{"a running job is asked to stop", alice, "2", 200, `"status":"cancelling"`},
		{"a finished job cannot be cancelled", alice, "3", 409, "can no longer be cancelled"},
		{"his job is not found for her", alice, "4", 404, "not found"},
		{"her job is not found for him", bob, "2", 404, "not found"},
	} {
		status, body := cancel(c.caller, c.id)
		if status != c.status || !strings.Contains(body, c.says) {
			t.Errorf("%s: %d %s", c.name, status, body)
		}
	}
	var stillQueued int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM deliverable_jobs WHERE id = 4 AND status = 'queued'`).Scan(&stillQueued); err != nil || stillQueued != 1 {
		t.Error("her requests changed his job")
	}
}
