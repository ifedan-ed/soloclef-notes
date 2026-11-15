package presencebeat

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/valkey-io/valkey-go"

	"git.danvics.com/danvics/soloclef/soloclef_notify/presence"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
)

const alice = "11111111-1111-4111-8111-111111111111"

// memoryMarks is Marks in memory.
type memoryMarks struct {
	mu    sync.Mutex
	marks map[string]time.Time
	err   error
}

func (m *memoryMarks) Mark(_ context.Context, userID, clientID string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.marks[userID+"/"+clientID] = now
	return nil
}

func (m *memoryMarks) Unmark(_ context.Context, userID, clientID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.marks, userID+"/"+clientID)
	return m.err
}

func serve(t *testing.T, marks Marks, method principal.Method, body string) *httptest.ResponseRecorder {
	t.Helper()
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	Route{Marks: marks, Errors: errorenvelope.Writer{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}.Register(api)
	request := httptest.NewRequest(http.MethodPut, Path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(principal.WithPrincipal(request.Context(), principal.Principal{UserID: alice, Method: method}))
	recorder := httptest.NewRecorder()
	api.Adapter().ServeHTTP(recorder, request)
	return recorder
}

func TestABeatMarksAndAHiddenTabUnmarks(t *testing.T) {
	marks := &memoryMarks{marks: map[string]time.Time{}}
	if got := serve(t, marks, principal.MethodSession, `{"client_id":"tab-abcdef12","active":true}`); got.Code != http.StatusNoContent {
		t.Fatalf("a beat answered %d %s", got.Code, got.Body)
	}
	if _, marked := marks.marks[alice+"/tab-abcdef12"]; !marked {
		t.Fatal("the beat left no mark")
	}
	if got := serve(t, marks, principal.MethodSession, `{"client_id":"tab-abcdef12","active":false}`); got.Code != http.StatusNoContent {
		t.Fatalf("hiding answered %d %s", got.Code, got.Body)
	}
	if len(marks.marks) != 0 {
		t.Error("the hidden tab is still marked")
	}
}

// A body that is not a beat is refused and marks nothing; a key or an
// agent is not a person at a screen; a Valkey that is away is a 503 the
// client ignores, not a 500.
func TestWhatIsNotABeatIsRefused(t *testing.T) {
	marks := &memoryMarks{marks: map[string]time.Time{}}
	for _, body := range []string{
		`{"client_id":"short","active":true}`,
		`{"client_id":"has spaces in it","active":true}`,
		`{"client_id":"tab-abcdef12"}`,
		`{"client_id":"tab-abcdef12","active":"yes"}`,
		`not json`,
		`{"client_id":"` + strings.Repeat("a", 65) + `","active":true}`,
	} {
		if got := serve(t, marks, principal.MethodSession, body); got.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s answered %d", body, got.Code)
		}
	}
	if got := serve(t, marks, principal.MethodPersonalAccessToken, `{"client_id":"tab-abcdef12","active":true}`); got.Code != http.StatusForbidden {
		t.Errorf("an API key answered %d", got.Code)
	}
	if len(marks.marks) != 0 {
		t.Errorf("marked %v", marks.marks)
	}
	marks.err = errors.New("valkey away")
	if got := serve(t, marks, principal.MethodSession, `{"client_id":"tab-abcdef12","active":true}`); got.Code != http.StatusServiceUnavailable {
		t.Errorf("a Valkey that is away answered %d", got.Code)
	}
}

// Against a real Valkey: a mark counts in the window the worker reads
// (ZCOUNT from now, as delivery's Presence asks), stops counting once it
// runs out, and two tabs are two marks so hiding one keeps the other.
// Opt-in: SOLOCLEF_API_TEST_PRESENCE_VALKEY names a throwaway server.
func TestMarksAreWhatTheWorkerCounts(t *testing.T) {
	address := os.Getenv("SOLOCLEF_API_TEST_PRESENCE_VALKEY")
	if address == "" {
		t.Skip("SOLOCLEF_API_TEST_PRESENCE_VALKEY is not set; this test needs a throwaway Valkey")
	}
	store, err := NewValkey(address)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	client, err := valkey.NewClient(valkey.MustParseURL(address))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx := context.Background()
	user := "presence-test-" + time.Now().Format("150405.000000")
	defer client.Do(ctx, client.B().Del().Key(presence.Key(user)).Build())
	count := func(at time.Time) int64 {
		n, err := client.Do(ctx, client.B().Zcount().Key(presence.Key(user)).Min(presence.LiveFrom(at)).Max("+inf").Build()).AsInt64()
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	now := time.Now()
	if err := store.Mark(ctx, user, "tab-one-1", now); err != nil {
		t.Fatal(err)
	}
	if err := store.Mark(ctx, user, "tab-two-2", now); err != nil {
		t.Fatal(err)
	}
	if count(now) != 2 || count(now.Add(presence.TTL-time.Second)) != 2 || count(now.Add(presence.TTL)) != 0 {
		t.Errorf("counts: now %d, before the TTL %d, at it %d", count(now), count(now.Add(presence.TTL-time.Second)), count(now.Add(presence.TTL)))
	}
	if err := store.Unmark(ctx, user, "tab-one-1"); err != nil {
		t.Fatal(err)
	}
	if count(now) != 1 {
		t.Error("hiding one tab hid the other")
	}
	ttl, _ := client.Do(ctx, client.B().Pttl().Key(presence.Key(user)).Build()).AsInt64()
	if ttl <= 0 || ttl > presence.TTL.Milliseconds() {
		t.Errorf("the set expires in %d ms", ttl)
	}
}
