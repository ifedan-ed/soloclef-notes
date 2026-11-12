package publiclinks

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/corsmirror"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

const (
	origin   = "https://notes.example"
	frontend = "https://notes.example.invalid"
	callerID = "11111111-2222-4333-8444-555555555555"
	// strangerID is a second account, which has made no links.
	strangerID = "99999999-2222-4333-8444-555555555555"
)

// fake is the database for one test: links, each with its owner. Both
// statements are handed the account they are run for, as the real ones are,
// and touch only that account's links.
type fake struct {
	links []Link
	down  bool
}

func (f *fake) Links(_ context.Context, userID string) ([]Link, error) {
	if f.down {
		return nil, errors.New("the database is unreachable")
	}
	mine := []Link{}
	for _, link := range f.links {
		if link.OwnerID == userID {
			mine = append(mine, link)
		}
	}
	return mine, nil
}

func (f *fake) Revoke(_ context.Context, userID string, linkID int64) (bool, error) {
	if f.down {
		return false, errors.New("the database is unreachable")
	}
	for index, link := range f.links {
		if link.ID == linkID && link.OwnerID == userID {
			f.links = append(f.links[:index], f.links[index+1:]...)
			return true, nil
		}
	}
	return false, nil
}

type answered struct {
	status int
	header http.Header
	raw    []byte
}

func (a answered) message(t *testing.T) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(a.raw, &envelope); err != nil || envelope.Error.RequestID != "req_test" {
		t.Fatalf("not this tier's envelope: %s", a.raw)
	}
	return envelope.Error.Message
}

// ask sends one request as the given account, to the two routes mounted
// behind the request-id and CORS middlewares as cmd/api mounts them.
func ask(t *testing.T, f *fake, userID string, method principal.Method, verb, path string) answered {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	envelope := errorenvelope.Writer{ReportURL: origin + "/contact"}
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "0.0.1"))
	ListRoute{Links: f, FrontendURL: frontend + "/", Errors: envelope, Logger: logger}.Register(api)
	RevokeRoute{Links: f, Errors: envelope, Logger: logger}.Register(api)
	withCaller := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mux.ServeHTTP(response, request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: userID, Method: method})))
	})
	server := httptest.NewServer(requestcontext.Middleware(logger,
		corsmirror.NewPolicy([]string{origin}).Middleware(withCaller)))
	defer server.Close()

	request, err := http.NewRequest(verb, server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Request-ID", "req_test")
	request.Header.Set("Origin", origin)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return answered{status: response.StatusCode, header: response.Header, raw: raw}
}

func pointer[T any](value T) *T { return &value }

// shared is two links of the caller's: a chat that never expires, and a
// studio output whose link lapsed long ago. Every title is invented.
func shared() []Link {
	return []Link{
		{ID: 302, ShareToken: "tok-302", OwnerID: callerID,
			CreatedAt:        time.Date(2026, 3, 1, 0, 0, 5, 250_000_000, time.UTC),
			ExpiresAt:        pointer(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
			NotebookOutputID: pointer(int64(40)), NotebookID: pointer(int64(7)),
			StoredTitle: "An output — “quoted” <&>"},
		{ID: 300, ShareToken: "tok-300", OwnerID: callerID,
			CreatedAt: time.Date(2026, 3, 1, 0, 0, 4, 0, time.UTC),
			ThreadID:  pointer(int64(12)), ThreadTitle: pointer("The chat as it is titled now"), Messages: 3},
	}
}

func TestTheListIsEveryLinkInTheShapeThePanelReads(t *testing.T) {
	got := ask(t, &fake{links: shared()}, callerID, principal.MethodSession, http.MethodGet, ListPath)
	const want = `{"snapshots":[` +
		`{"id":302,"share_token":"tok-302","public_url":"https://notes.example.invalid/public/tok-302",` +
		`"created_at":"2026-03-01T00:00:05.250000Z","expires_at":"2026-01-01T00:00:00Z","is_expired":true,` +
		`"kind":"output","message_count":0,"thread_id":null,"thread_title":"An output — “quoted” <&>",` +
		`"notebook_id":7,"created_by_user_id":"` + callerID + `"},` +
		`{"id":300,"share_token":"tok-300","public_url":"https://notes.example.invalid/public/tok-300",` +
		`"created_at":"2026-03-01T00:00:04Z","expires_at":null,"is_expired":false,` +
		`"kind":"chat","message_count":3,"thread_id":12,"thread_title":"The chat as it is titled now",` +
		`"notebook_id":null,"created_by_user_id":"` + callerID + `"}]}`
	if got.status != http.StatusOK || string(got.raw) != want {
		t.Errorf("status %d\n got  %s\n want %s", got.status, got.raw, want)
	}
}

// The title frozen into the copy, then the chat's own, then "Untitled"; an
// empty title is no title.
func TestWhatALinkIsListedAs(t *testing.T) {
	for name, c := range map[string]struct {
		link Link
		want string
	}{
		"the frozen title wins":             {Link{StoredTitle: "Frozen", ThreadTitle: pointer("Now")}, "Frozen"},
		"no frozen title: the chat's":       {Link{ThreadTitle: pointer("Now")}, "Now"},
		"an empty chat title is no title":   {Link{ThreadTitle: pointer("")}, "Untitled"},
		"a link whose chat is gone":         {Link{}, "Untitled"},
		"an output with a title of its own": {Link{StoredTitle: "A deck", NotebookOutputID: pointer(int64(1))}, "A deck"},
	} {
		if got := c.link.Title(); got != c.want {
			t.Errorf("%s: listed as %q, want %q", name, got, c.want)
		}
	}
}

func TestALinkExpiresAtItsInstantAndNotBefore(t *testing.T) {
	at := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	for name, c := range map[string]struct {
		expires *time.Time
		want    bool
	}{
		"no expiry":           {nil, false},
		"a microsecond early": {pointer(at.Add(time.Microsecond)), false},
		"the exact instant":   {pointer(at), true},
		"long ago":            {pointer(at.Add(-time.Hour)), true},
	} {
		if got := Expired(c.expires, at); got != c.want {
			t.Errorf("%s: expired %v, want %v", name, got, c.want)
		}
	}
}

// The isolation test of the two routes. A link is listed for its owner and
// nobody else, and another account can no more revoke it than a link that
// was never made: the same 404, and the link still there.
func TestALinkIsItsOwnersAndNobodyElses(t *testing.T) {
	for _, method := range []principal.Method{
		principal.MethodSession, principal.MethodPersonalAccessToken, principal.MethodAgent,
	} {
		f := &fake{links: shared()}
		got := ask(t, f, strangerID, method, http.MethodGet, ListPath)
		if got.status != http.StatusOK || string(got.raw) != `{"snapshots":[]}` {
			t.Errorf("another account's list by %s: %d %s", method, got.status, got.raw)
		}
		got = ask(t, f, strangerID, method, http.MethodDelete, "/api/v1/snapshots/300")
		if got.status != http.StatusNotFound || got.message(t) != linkNotFound || len(f.links) != 2 {
			t.Errorf("another account's revoke by %s: %d %s, %d links left", method, got.status, got.raw, len(f.links))
		}
		// However the owner signed in, it is theirs to take down.
		got = ask(t, f, callerID, method, http.MethodDelete, "/api/v1/snapshots/300")
		if got.status != http.StatusOK || string(got.raw) != `{"message":"Public link revoked"}` || len(f.links) != 1 {
			t.Errorf("the owner's revoke by %s: %d %s, %d links left", method, got.status, got.raw, len(f.links))
		}
	}
}

func TestARevokeOfNothingIsNotFound(t *testing.T) {
	for name, c := range map[string]struct {
		id     string
		status int
	}{
		"a link that was never made":     {"999", 404},
		"an id wider than any id":        {"99999999999999999999999", 404},
		"an id that is not a number":     {"latest", 422},
		"a link that was revoked before": {"300", 404},
	} {
		f := &fake{links: shared()[:1]}
		got := ask(t, f, callerID, principal.MethodSession, http.MethodDelete, "/api/v1/snapshots/"+c.id)
		if got.status != c.status || len(f.links) != 1 {
			t.Errorf("%s: status %d, want %d: %s", name, got.status, c.status, got.raw)
		}
		if c.status == http.StatusNotFound && got.message(t) != linkNotFound {
			t.Errorf("%s: told %q", name, got.message(t))
		}
	}
}

// A failure nobody expected is the tier's 500, and a browser can read it.
func TestAFailureKeepsItsCORSHeaders(t *testing.T) {
	for _, route := range []struct{ verb, path string }{
		{http.MethodGet, ListPath}, {http.MethodDelete, "/api/v1/snapshots/300"},
	} {
		got := ask(t, &fake{down: true}, callerID, principal.MethodSession, route.verb, route.path)
		if got.status != http.StatusInternalServerError || got.header.Get("Access-Control-Allow-Origin") != origin {
			t.Errorf("%s %s: status %d, headers %v", route.verb, route.path, got.status, got.header)
		}
		if got.message(t) != errorenvelope.GenericServerMessage {
			t.Errorf("%s %s: told %q", route.verb, route.path, got.message(t))
		}
	}
}
