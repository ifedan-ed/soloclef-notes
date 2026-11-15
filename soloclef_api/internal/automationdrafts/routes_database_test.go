package automationdrafts

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

const databaseVariable = "SOLOCLEF_API_TEST_AUTOMATIONDRAFTS_DATABASE"

// A draft is read and discarded by its owner alone, and only a waiting one
// can be discarded; the card is told the folder its trigger waits on.
func TestADraftIsItsOwnersAlone(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	ctx := context.Background()
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	routes := Routes{Pool: pool, Errors: errorenvelope.Writer{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := routes.Check(); err != nil {
		t.Fatal(err)
	}
	routes.Register(api)
	call := func(caller, method, path string) (int, string) {
		request := httptest.NewRequest(method, path, nil)
		request = request.WithContext(principal.WithPrincipal(request.Context(), principal.Principal{UserID: caller, Method: principal.MethodSession}))
		recorder := httptest.NewRecorder()
		api.Adapter().ServeHTTP(recorder, request)
		return recorder.Code, recorder.Body.String()
	}
	var folder int64
	if err := pool.QueryRow(ctx, `INSERT INTO folders (user_id, name, "position") VALUES ($1, 'Papers', 'a0') RETURNING id`, alice).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	request := `{"name":"On arrival","definition":{"name":"On arrival","plan":[]},"triggers":[{"type":"event","params":{"event_type":"document.entered_folder","filter":{"folder_id":` +
		strconv.FormatInt(folder, 10) + `}}}]}`
	var id string
	if err := pool.QueryRow(ctx, `INSERT INTO automation_drafts (user_id, request) VALUES ($1, $2) RETURNING id`, alice, request).Scan(&id); err != nil {
		t.Fatal(err)
	}
	one := "/api/v1/automation-drafts/" + id

	status, body := call(alice, "GET", one)
	var view struct {
		Status  string          `json:"status"`
		Request json.RawMessage `json:"request"`
		Folders []folderView    `json:"folders"`
		AutoID  *int64          `json:"automation_id"`
	}
	if status != http.StatusOK || json.Unmarshal([]byte(body), &view) != nil {
		t.Fatalf("read: %d %s", status, body)
	}
	if view.Status != "pending" || view.AutoID != nil || len(view.Folders) != 1 || view.Folders[0].Title != "Papers" || !strings.Contains(string(view.Request), `"On arrival"`) {
		t.Errorf("view = %+v", view)
	}
	// Bob reads and discards nothing of Alice's; a malformed id names no draft.
	for _, c := range []struct{ caller, method, path string }{
		{bob, "GET", one}, {bob, "POST", one + "/discard"},
		{alice, "GET", "/api/v1/automation-drafts/not-a-draft"}, {alice, "POST", "/api/v1/automation-drafts/1/discard"},
	} {
		if status, body := call(c.caller, c.method, c.path); status != http.StatusNotFound || !strings.Contains(body, draftAbsent) {
			t.Errorf("%s %s as %s: %d %s", c.method, c.path, c.caller, status, body)
		}
	}
	if status, body := call(alice, "POST", one+"/discard"); status != http.StatusOK || !strings.Contains(body, `"status":"discarded"`) {
		t.Fatalf("discard: %d %s", status, body)
	}
	// Discarded is final: neither Discard again nor anything else revives it.
	if status, body := call(alice, "POST", one+"/discard"); status != http.StatusConflict || !strings.Contains(body, draftNotWaiting) {
		t.Errorf("a second discard: %d %s", status, body)
	}

	// A created draft shows the automation as saved, which the person may
	// have changed on the card: here a new name and a 09:30 schedule.
	var automation int64
	if err := pool.QueryRow(ctx, `INSERT INTO automations (user_id, name, definition) VALUES ($1, 'Renamed on the card', '{}') RETURNING id`, alice).Scan(&automation); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO automation_triggers (automation_id, type, params) VALUES ($1, 'schedule', '{"cron":"30 9 * * 1","timezone":"Africa/Lagos"}')`, automation); err != nil {
		t.Fatal(err)
	}
	var created string
	if err := pool.QueryRow(ctx, `INSERT INTO automation_drafts (user_id, request, status, automation_id) VALUES ($1, $2, 'created', $3) RETURNING id`, alice, request, automation).Scan(&created); err != nil {
		t.Fatal(err)
	}
	status, body = call(alice, "GET", "/api/v1/automation-drafts/"+created)
	if status != http.StatusOK || !strings.Contains(body, `"automation":{"name":"Renamed on the card","triggers":[{"type":"schedule","params":{"cron":"30 9 * * 1","timezone":"Africa/Lagos"}}]}`) {
		t.Errorf("a created draft: %d %s", status, body)
	}
	// Once the automation is deleted the draft stays created, with nothing to show.
	if _, err := pool.Exec(ctx, `DELETE FROM automations WHERE id = $1`, automation); err != nil {
		t.Fatal(err)
	}
	if status, body = call(alice, "GET", "/api/v1/automation-drafts/"+created); !strings.Contains(body, `"status":"created"`) || !strings.Contains(body, `"automation":null`) {
		t.Errorf("after the automation was deleted: %d %s", status, body)
	}
}
