package clienterrors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
)

// counts is a counter in memory; down makes every hit fail.
type counts struct {
	hits map[string]int64
	down bool
}

func (c *counts) Hit(_ context.Context, key string, _ time.Duration, cost int64) (int64, error) {
	if c.down {
		return 0, errors.New("the counter is down")
	}
	c.hits[key] += cost
	return c.hits[key], nil
}

const (
	alice = "11111111-1111-4111-8111-111111111111"
	bob   = "22222222-2222-4222-8222-222222222222"
)

func TestACrashReportBecomesOneBoundedLogLine(t *testing.T) {
	var log bytes.Buffer
	budget := &counts{hits: map[string]int64{}}
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	route := Route{Budget: budget, Errors: errorenvelope.Writer{}, Logger: slog.New(slog.NewJSONHandler(&log, nil))}
	if err := route.Check(); err != nil {
		t.Fatal(err)
	}
	route.Register(api)
	post := func(userID string, how principal.Method, body string) (int, string) {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, Path, strings.NewReader(body))
		request = request.WithContext(principal.WithPrincipal(request.Context(), principal.Principal{UserID: userID, Method: how}))
		recorder := httptest.NewRecorder()
		api.Adapter().ServeHTTP(recorder, request)
		return recorder.Code, recorder.Body.String()
	}
	lines := func() []map[string]any {
		t.Helper()
		var read []map[string]any
		for _, raw := range strings.Split(strings.TrimSpace(log.String()), "\n") {
			if raw == "" {
				continue
			}
			var line map[string]any
			if err := json.Unmarshal([]byte(raw), &line); err != nil {
				t.Fatalf("the log holds a line that is not one JSON object: %q", raw)
			}
			read = append(read, line)
		}
		return read
	}

	// A report whose every field tries to write a second log line, carry
	// a token or run long.
	frames := strings.Repeat("  at frame (app.js:1:1)\n\n", 30)
	body, _ := json.Marshal(map[string]string{
		"kind":       "error-boundary",
		"name":       "TypeError",
		"message":    "x is undefined\n{\"level\":\"ERROR\",\"msg\":\"forged\"}" + strings.Repeat("m", 2000),
		"stack":      frames,
		"path":       "/dashboard/notebooks/4?token=secret#access=also-secret",
		"digest":     "d1",
		"build":      "abc123",
		"request_id": "req-9",
	})
	if status, said := post(alice, principal.MethodSession, string(body)); status != http.StatusNoContent || said != "" {
		t.Fatalf("a report answered %d %s", status, said)
	}
	written := lines()
	if len(written) != 1 {
		t.Fatalf("one report wrote %d lines: %s", len(written), log.String())
	}
	line := written[0]
	if line["level"] != "WARN" || line["msg"] != "client error" || line["user_id"] != alice || line["kind"] != "error-boundary" ||
		line["build"] != "abc123" || line["digest"] != "d1" || line["failed_request"] != "req-9" || line["name"] != "TypeError" {
		t.Errorf("the line reads %v", line)
	}
	if line["path"] != "/dashboard/notebooks/4" {
		t.Errorf("the path kept its query or fragment: %v", line["path"])
	}
	if message := line["message"].(string); len(message) != messageMax || !strings.HasPrefix(message, "x is undefined {") {
		t.Errorf("the message is %d characters: %.60s", len(message), message)
	}
	if stack := line["stack"].([]any); len(stack) != stackLines || stack[0] != "at frame (app.js:1:1)" {
		t.Errorf("the stack holds %d frames: %v", len(stack), stack)
	}
	if strings.Contains(log.String(), "secret") {
		t.Error("the log holds what was in the query string")
	}

	// Refusals, none of which writes a report.
	log.Reset()
	for _, c := range []struct {
		name string
		how  principal.Method
		body string
		want int
	}{
		{"an API key", principal.MethodPersonalAccessToken, string(body), http.StatusForbidden},
		{"an agent", principal.MethodAgent, string(body), http.StatusForbidden},
		{"a body over the cap", principal.MethodSession, `{"kind":"window-error","stack":"` + strings.Repeat("s", maxBodyBytes) + `"}`, http.StatusRequestEntityTooLarge},
		{"not JSON", principal.MethodSession, `kind=window-error`, http.StatusUnprocessableEntity},
		{"an unknown kind", principal.MethodSession, `{"kind":"anything"}`, http.StatusUnprocessableEntity},
		{"no kind", principal.MethodSession, `{}`, http.StatusUnprocessableEntity},
	} {
		if status, said := post(alice, c.how, c.body); status != c.want {
			t.Errorf("%s answered %d %s, want %d", c.name, status, said, c.want)
		}
	}
	if log.Len() != 0 {
		t.Errorf("a refused report was logged: %s", log.String())
	}
	// A key is refused before it is counted; an unreadable report is not.
	if got := budget.hits["client-errors:account:"+alice]; got != 4 {
		t.Errorf("her budget counts %d hits, want 4 (one report, three unreadable)", got)
	}
	// A report of exactly the cap is read.
	exact := `{"kind":"window-error","stack":"` + strings.Repeat("s", maxBodyBytes-len(`{"kind":"window-error","stack":""}`)) + `"}`
	if status, said := post(bob, principal.MethodSession, exact); status != http.StatusNoContent {
		t.Errorf("a report of exactly the cap answered %d %s", status, said)
	}
}

func TestTheBudgetsStopALoopingPageAndACrowd(t *testing.T) {
	var log bytes.Buffer
	budget := &counts{hits: map[string]int64{}}
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	Route{Budget: budget, Errors: errorenvelope.Writer{}, Logger: slog.New(slog.NewJSONHandler(&log, nil))}.Register(api)
	post := func(userID string) (int, http.Header) {
		request := httptest.NewRequest(http.MethodPost, Path, strings.NewReader(`{"kind":"window-error","message":"boom"}`))
		request = request.WithContext(principal.WithPrincipal(request.Context(), principal.Principal{UserID: userID, Method: principal.MethodSession}))
		recorder := httptest.NewRecorder()
		api.Adapter().ServeHTTP(recorder, request)
		return recorder.Code, recorder.Header()
	}
	for i := 1; i <= perAccount; i++ {
		if status, _ := post(alice); status != http.StatusNoContent {
			t.Fatalf("her report %d answered %d", i, status)
		}
	}
	status, headers := post(alice)
	if status != http.StatusTooManyRequests || headers.Get("Retry-After") != "60" {
		t.Errorf("her report past the budget answered %d, Retry-After %q", status, headers.Get("Retry-After"))
	}
	// Her refused reports did not spend the site's budget, and he still
	// has his own.
	for range 5 {
		post(alice)
	}
	if got := budget.hits["client-errors:site"]; got != perAccount {
		t.Errorf("the site's budget counts %d, want %d", got, perAccount)
	}
	if status, _ := post(bob); status != http.StatusNoContent {
		t.Errorf("his first report answered %d", status)
	}
	// The site's budget, spent by many accounts, refuses the next one.
	budget.hits["client-errors:site"] = acrossTheSite
	if status, _ := post(bob); status != http.StatusTooManyRequests {
		t.Errorf("a report past the site's budget answered %d", status)
	}
	if got := strings.Count(log.String(), `"msg":"client error"`); got != perAccount+1 {
		t.Errorf("%d reports were logged, want %d", got, perAccount+1)
	}
	// With the counter down nothing is taken: an uncounted report is an
	// unbounded one.
	budget.down = true
	if status, _ := post(bob); status != http.StatusServiceUnavailable {
		t.Errorf("a report that could not be counted answered %d", status)
	}
	if err := (Route{}).Check(); err == nil {
		t.Error("a route with no counter passed its check")
	}
}
