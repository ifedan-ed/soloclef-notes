package deliverablejobs

import (
	"context"
	"errors"
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

// published is one message the route asked the queue for.
type published struct {
	jobID  int64
	taskID string
}

func TestAJobIsRetriedByItsOwnerAlone(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	// Job 1 failed on its second attempt, after a takeover: everything an
	// ended attempt leaves behind is on the row, to see that a retry clears
	// it. Job 3 failed after its video was saved, which a retry must not
	// make twice.
	testpostgres.Exec(t, pool, `
INSERT INTO documents (id, user_id, title, document_type, content, content_hash, status) VALUES
 (30, '`+alice+`', 'Made', 'ARTIFACT', '', 'h30', '{"state":"ready"}');
INSERT INTO artifacts (id, document_id, user_id, format) VALUES (31, 30, '`+alice+`', 'video');
INSERT INTO deliverable_jobs (id, user_id, kind, title, tool_call_id, status, attempt_count, celery_task_id,
                              phase, progress, checkpoint, failure_code, internal_error,
                              cancel_requested_at, claimed_at, heartbeat_at, finished_at, artifact_id) VALUES
 (1, '`+alice+`', 'video', 'Failed', 'a', 'failed', 2, 'deliverable-job:1:attempt:2',
     'failed', 40, '{"orphan_takeovers": 2}', 'render_failed', 'boom', now(), now(), now(), now(), NULL),
 (2, '`+alice+`', 'video', 'Cancelled', 'b', 'cancelled', 1, 'deliverable-job:2:attempt:1',
     'cancelled', 0, '{}', 'cancelled', NULL, now(), NULL, NULL, now(), NULL),
 (3, '`+alice+`', 'video', 'Saved', 'c', 'failed', 1, 'deliverable-job:3:attempt:1',
     'failed', 90, '{}', 'render_failed', NULL, NULL, NULL, NULL, now(), 31),
 (4, '`+alice+`', 'video', 'Running', 'd', 'running', 1, 'deliverable-job:4:attempt:1',
     'rendering', 50, '{}', NULL, NULL, NULL, now(), now(), NULL, NULL),
 (5, '`+alice+`', 'report', 'A kind nothing makes', 'e', 'failed', 1, 'deliverable-job:5:attempt:1',
     'failed', 0, '{}', 'generation_failed', NULL, NULL, NULL, NULL, now(), NULL),
 (6, '`+bob+`',   'video', 'His', 'f', 'failed', 1, 'deliverable-job:6:attempt:1',
     'failed', 0, '{}', 'render_failed', NULL, NULL, NULL, NULL, now(), NULL);`)

	var sent []published
	var brokerDown bool
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	clock := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	route := RetryRoute{Database: pool, Errors: errorenvelope.Writer{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return clock },
		Publish: func(_ context.Context, jobID int64, taskID string) error {
			if brokerDown {
				return errors.New("the broker is down")
			}
			sent = append(sent, published{jobID, taskID})
			return nil
		}}
	if err := route.Check(); err != nil {
		t.Fatal(err)
	}
	route.Register(api)
	retry := func(userID, id string) (int, string) {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/deliverable-jobs/"+id+"/retry", nil)
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
		{"his job is not found for her", alice, "6", 404, "not found"},
		{"her job is not found for him", bob, "1", 404, "not found"},
		{"a failed job is queued again", alice, "1", 200, `"status":"queued"`},
		{"asking again answers it as it is", alice, "1", 200, `"status":"queued"`},
		{"a job with its video is not made twice", alice, "3", 409, "not retryable"},
		{"a running job is not retried", alice, "4", 409, "not retryable"},
		{"a kind nothing makes is not queued", alice, "5", 409, "not retryable"},
		{"an id that is not a number is refused", alice, "x", 422, "valid integer"},
	} {
		status, body := retry(c.caller, c.id)
		if status != c.status || !strings.Contains(body, c.says) {
			t.Errorf("%s: %d %s", c.name, status, body)
		}
		if strings.Contains(body, "deliverable-job:") || strings.Contains(body, "boom") {
			t.Errorf("%s: the answer carries the task id or the internal error: %s", c.name, body)
		}
	}
	// One message for the one new attempt, under the id the worker claims
	// it by; the second click published nothing.
	if len(sent) != 1 || sent[0] != (published{1, "deliverable-job:1:attempt:3"}) {
		t.Fatalf("published %+v", sent)
	}
	var (
		attempt                               int
		taskID, checkpoint                    string
		leftovers                             int
		updatedAt                             time.Time
		phase, failureCode, internalError     *string
		cancelAt, claimedAt, heartbeat, ended *time.Time
	)
	if err := pool.QueryRow(context.Background(), `
SELECT attempt_count, celery_task_id, checkpoint::text, progress, updated_at, phase, failure_code, internal_error,
       cancel_requested_at, claimed_at, heartbeat_at, finished_at
FROM deliverable_jobs WHERE id = 1`).Scan(&attempt, &taskID, &checkpoint, &leftovers, &updatedAt, &phase, &failureCode,
		&internalError, &cancelAt, &claimedAt, &heartbeat, &ended); err != nil {
		t.Fatal(err)
	}
	if attempt != 3 || taskID != "deliverable-job:1:attempt:3" || checkpoint != "{}" || leftovers != 0 || !updatedAt.Equal(clock) ||
		phase != nil || failureCode != nil || internalError != nil || cancelAt != nil || claimedAt != nil || heartbeat != nil || ended != nil {
		t.Errorf("the ended attempt left something on the row: attempt %d, task %q, checkpoint %s, progress %d, updated %s",
			attempt, taskID, checkpoint, leftovers, updatedAt)
	}

	// A broker that is down does not fail the retry: the row is queued under
	// its new task id, which is all the worker's reconciler needs to
	// publish it later.
	brokerDown = true
	if status, body := retry(alice, "2"); status != 200 || !strings.Contains(body, `"status":"queued"`) {
		t.Errorf("with the broker down: %d %s", status, body)
	}
	var waiting int
	if err := pool.QueryRow(context.Background(), `
SELECT count(*) FROM deliverable_jobs
WHERE id = 2 AND status = 'queued' AND attempt_count = 2 AND celery_task_id = 'deliverable-job:2:attempt:2'`).Scan(&waiting); err != nil || waiting != 1 {
		t.Error("an unpublished retry is not left queued for the reconciler")
	}

	var untouched int
	if err := pool.QueryRow(context.Background(), `
SELECT count(*) FROM deliverable_jobs
WHERE (id = 6 AND status = 'failed' AND attempt_count = 1)
   OR (id IN (3, 5) AND status = 'failed' AND attempt_count = 1)
   OR (id = 4 AND status = 'running')`).Scan(&untouched); err != nil || untouched != 4 {
		t.Errorf("a refused retry changed a job: %d of 4 are as they were", untouched)
	}
}
