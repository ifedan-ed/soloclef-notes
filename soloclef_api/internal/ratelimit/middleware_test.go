package ratelimit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
)

// testErrors writes the envelope with a fixed clock, so a body compared in a
// test is the same bytes every run.
func testErrors() errorenvelope.Writer {
	return errorenvelope.Writer{
		ReportURL: "https://notes.example.invalid/contact",
		Now:       func() time.Time { return time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC) },
	}
}

type countingCounter struct{ count int64 }

func (c *countingCounter) Hit(context.Context, string, time.Duration, int64) (int64, error) {
	c.count++
	return c.count, nil
}

// The refusal arrives on the request after the budget, not on the one that
// spends the last of it: FixedWindowRateLimiter.hit allows a count equal to the
// amount.
func TestTheRefusalArrivesOnTheRequestPastTheBudget(t *testing.T) {
	counter := &countingCounter{}
	served := 0
	limiter := Middleware{
		Counter: counter,
		Limit:   Limit{Amount: 3, Multiples: 1, Granularity: "minute", Window: time.Minute},
		Covers:  CoveringRoutes("GET /api/v1/workspaces/{workspace_id}/members"),
		Errors:  testErrors(),
		Logger:  quietLogger(),
	}
	handler := limiter.Wrap(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		served++
		response.WriteHeader(http.StatusOK)
	}))

	statuses := []int{}
	for attempt := 0; attempt < 4; attempt++ {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/1/members", nil)
		request.Header.Set("CF-Connecting-IP", "203.0.113.7")
		handler.ServeHTTP(recorder, request)
		statuses = append(statuses, recorder.Code)
		if recorder.Code == http.StatusTooManyRequests {
			if got := recorder.Header().Get("Retry-After"); got != "1 minute" {
				t.Errorf("retry-after: got %q", got)
			}
		}
	}
	want := []int{200, 200, 200, 429}
	for index, status := range want {
		if statuses[index] != status {
			t.Fatalf("got %v, want %v", statuses, want)
		}
	}
	if served != 3 {
		t.Errorf("the route ran %d times, want 3", served)
	}
}

// A request no route of this tier answers is not counted, because the Python
// tier's limiter does not count a request none of its routes match either.
func TestOnlyTheRoutesThisTierServesAreCounted(t *testing.T) {
	counter := &countingCounter{}
	limiter := Middleware{
		Counter: counter,
		Limit:   DefaultLimit,
		Covers: CoveringRoutes(
			"GET /api/v1/workspaces/{workspace_id}/members",
			"GET /api/v1/workspaces/{workspace_id}/my-access"),
		Errors: testErrors(),
		Logger: quietLogger(),
	}
	handler := limiter.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	cases := map[string]struct {
		method, path string
		counted      bool
	}{
		"a route this tier serves":         {http.MethodGet, "/api/v1/workspaces/1/members", true},
		"a method it does not serve":       {http.MethodPut, "/api/v1/workspaces/1/members/7", false},
		"a path no route of its own takes": {http.MethodGet, "/api/v1/documents", false},
		"a member of the member list":      {http.MethodGet, "/api/v1/workspaces/1/members/7", false},
	}
	for name, test := range cases {
		before := counter.count
		request := httptest.NewRequest(test.method, test.path, nil)
		request.Header.Set("CF-Connecting-IP", "203.0.113.7")
		handler.ServeHTTP(httptest.NewRecorder(), request)
		if counted := counter.count > before; counted != test.counted {
			t.Errorf("%s: counted=%v, want %v", name, counted, test.counted)
		}
	}
}

// A request whose client cannot be named is not counted, as
// __evaluate_limits skips a limit with an empty parameter.
func TestARequestWithNoNameableClientIsNotCounted(t *testing.T) {
	counter := &countingCounter{}
	limiter := Middleware{
		Counter: counter,
		Limit:   DefaultLimit,
		Covers:  CoveringRoutes("GET /api/v1/workspaces/{workspace_id}/members"),
		Errors:  testErrors(),
		Logger:  quietLogger(),
	}
	served := false
	handler := limiter.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { served = true }))

	request := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/1/members", nil)
	request.Header.Set("CF-Connecting-IP", "  ")
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if counter.count != 0 {
		t.Errorf("the request was counted against %d", counter.count)
	}
	if !served {
		t.Error("the request was refused rather than let through uncounted")
	}
}
