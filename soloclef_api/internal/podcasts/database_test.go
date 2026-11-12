package podcasts

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/deliverables"
)

// databaseVariable names a throwaway Postgres for this package's database
// tests. **Every account in it is deleted** (internal/testpostgres), so it
// must never name a database anyone uses. With it unset the tests are
// skipped.
const databaseVariable = "SOLOCLEF_API_TEST_PODCASTS_DATABASE"

// The two accounts of the seed (testdata/podcasts_seed.sql): Alice owns the
// studio's podcasts, Bob owns one of his own.
var accounts = map[string]string{
	"alice": testpostgres.Alice,
	"bob":   testpostgres.Bob,
}

// overPostgres is the eleven routes over the real Store, and a function
// that puts the seed's rows back as they were.
type overPostgres struct {
	pool   *pgxpool.Pool
	server *httptest.Server
	// booth is the queue, the catalogue and the sample booth: a stand-in,
	// because none of them is the database.
	booth *fake
	// caller is who the next request is made as.
	caller principal.Principal
	reseed func(t *testing.T)
}

func openOverPostgres(t *testing.T) *overPostgres {
	t.Helper()
	over := &overPostgres{pool: testpostgres.Open(t, databaseVariable)}
	seed, err := os.ReadFile(filepath.Join("testdata", "podcasts_seed.sql"))
	if err != nil {
		t.Fatalf("reading the seed: %v", err)
	}
	// The seed empties what it fills, so every case is asked of the same
	// rows, whatever the case before it wrote.
	over.reseed = func(t *testing.T) {
		t.Helper()
		testpostgres.Exec(t, over.pool, string(seed))
	}

	over.booth = &fake{catalogue: kokoro(t, "af_heart", "am_adam")}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mux := http.NewServeMux()
	register(humago.New(mux, huma.DefaultConfig("test", "0.0.1")), Store{Pool: over.pool},
		over.booth, over.booth, over.booth, errorenvelope.Writer{ReportURL: origin + "/contact"}, logger)
	// The body reader is where cmd/api puts it: outside the authentication.
	bodies := requestbody.Middleware{
		Covers: func(*http.Request) bool { return true },
	}
	over.server = httptest.NewServer(requestcontext.Middleware(logger, bodies.Wrap(
		http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			mux.ServeHTTP(response, request.WithContext(principal.WithPrincipal(request.Context(), over.caller)))
		}))))
	t.Cleanup(over.server.Close)
	return over
}

// TestTheRoutesOverARealDatabase asks the real Store -- the generated
// statements and the transaction seam -- what the in-memory tests ask a
// stand-in: that the queries find and write the rows they are meant to and
// **only their owner's**, that the enum cast works, that a guarded move
// really refuses a row that has moved, and that a delete leaves a notebook
// output pointing at nothing.
func TestTheRoutesOverARealDatabase(t *testing.T) {
	over := openOverPostgres(t)
	ctx := context.Background()
	pool, server, queue := over.pool, over.server, over.booth

	statusOf := func(id int64) string {
		var status string
		err := pool.QueryRow(ctx, "SELECT status::text FROM podcast_runs WHERE id = $1", id).Scan(&status)
		if err != nil {
			return "gone"
		}
		return status
	}

	const (
		detailOf   = "/api/v1/podcasts/%d"
		approve    = "/api/v1/podcasts/%d/brief/approve"
		regenerate = "/api/v1/podcasts/%d/transcript/regenerate"
		revert     = "/api/v1/podcasts/%d/regenerate/revert"
		cancelOf   = "/api/v1/podcasts/%d/cancel"
	)
	for _, c := range []struct {
		name       string
		as         string
		method     principal.Method
		verb, path string
		id         int64
		brokerDown bool
		status     int
		left       string // the podcast's status afterwards, or "gone"
		drafts     int
	}{
		{"the owner reads", "alice", principal.MethodSession, http.MethodGet, detailOf, 1301, false, 200, "ready", 0},
		{"the owner reads by an API key", "alice", principal.MethodPersonalAccessToken, http.MethodGet, detailOf, 1300, false, 200, "awaiting_brief", 0},
		{"no such podcast", "alice", principal.MethodSession, http.MethodGet, detailOf, 1399, false, 404, "gone", 0},
		{"an id past thirty-two bits is only a podcast that is not there", "alice", principal.MethodSession, http.MethodGet, detailOf, 2147483648, false, 404, "gone", 0},
		{"a brief nobody can read is still a podcast", "alice", principal.MethodSession, http.MethodGet, detailOf, 1307, false, 200, "awaiting_brief", 0},

		// The isolation cases. Each is the same request its owner is
		// answered 200 for just below, made by the other account: a 404,
		// the row untouched, nothing queued.
		{"another account cannot read", "bob", principal.MethodSession, http.MethodGet, detailOf, 1301, false, 404, "ready", 0},
		{"another account cannot approve", "bob", principal.MethodSession, http.MethodPost, approve, 1300, false, 404, "awaiting_brief", 0},
		{"another account cannot regenerate", "bob", principal.MethodSession, http.MethodPost, regenerate, 1301, false, 404, "ready", 0},
		{"another account cannot revert", "bob", principal.MethodSession, http.MethodPost, revert, 1302, false, 404, "drafting", 0},
		{"another account cannot cancel", "bob", principal.MethodSession, http.MethodPost, cancelOf, 1300, false, 404, "awaiting_brief", 0},
		{"another account cannot delete", "bob", principal.MethodSession, http.MethodDelete, detailOf, 1310, false, 404, "cancelled", 0},
		{"another account's key cannot delete", "bob", principal.MethodPersonalAccessToken, http.MethodDelete, detailOf, 1310, false, 404, "cancelled", 0},
		{"nor can the first account read the second's", "alice", principal.MethodSession, http.MethodGet, detailOf, 1309, false, 404, "awaiting_brief", 0},
		{"or cancel it", "alice", principal.MethodSession, http.MethodPost, cancelOf, 1309, false, 404, "awaiting_brief", 0},
		{"or delete it", "alice", principal.MethodSession, http.MethodDelete, detailOf, 1309, false, 404, "awaiting_brief", 0},
		{"which its owner can", "bob", principal.MethodSession, http.MethodDelete, detailOf, 1309, false, 200, "gone", 0},

		{"an approval", "alice", principal.MethodSession, http.MethodPost, approve, 1300, false, 200, "drafting", 1},
		{"no brief to approve", "alice", principal.MethodSession, http.MethodPost, approve, 1304, false, 422, "awaiting_brief", 0},
		{"an empty brief to approve", "alice", principal.MethodSession, http.MethodPost, approve, 1305, false, 422, "awaiting_brief", 0},
		{"an unreadable brief to approve", "alice", principal.MethodSession, http.MethodPost, approve, 1307, false, 422, "awaiting_brief", 0},
		{"approve a finished episode", "alice", principal.MethodSession, http.MethodPost, approve, 1301, false, 409, "ready", 0},
		{"approve with the broker down", "alice", principal.MethodSession, http.MethodPost, approve, 1300, true, 503, "awaiting_brief", 0},

		{"regenerate a finished episode", "alice", principal.MethodSession, http.MethodPost, regenerate, 1301, false, 200, "awaiting_brief", 0},
		{"regenerate from the old review gate", "alice", principal.MethodSession, http.MethodPost, regenerate, 1312, false, 200, "awaiting_brief", 0},
		{"regenerate an episode with no brief", "alice", principal.MethodSession, http.MethodPost, regenerate, 1311, false, 422, "ready", 0},
		{"regenerate what is not finished", "alice", principal.MethodSession, http.MethodPost, regenerate, 1300, false, 409, "awaiting_brief", 0},

		{"revert a regeneration", "alice", principal.MethodSession, http.MethodPost, revert, 1302, false, 200, "ready", 0},
		{"revert a first take", "alice", principal.MethodSession, http.MethodPost, revert, 1300, false, 409, "awaiting_brief", 0},

		{"a cancel at the gate", "alice", principal.MethodSession, http.MethodPost, cancelOf, 1300, false, 200, "cancelled", 0},
		{"a cancel of what is pending", "alice", principal.MethodSession, http.MethodPost, cancelOf, 1303, false, 200, "cancelled", 0},
		{"a cancel of what is cancelled already", "alice", principal.MethodSession, http.MethodPost, cancelOf, 1310, false, 200, "cancelled", 0},
		{"a finished episode is not cancelled", "alice", principal.MethodSession, http.MethodPost, cancelOf, 1301, false, 409, "ready", 0},
		{"nor is a new take of one", "alice", principal.MethodSession, http.MethodPost, cancelOf, 1302, false, 409, "drafting", 0},

		{"a delete", "alice", principal.MethodSession, http.MethodDelete, detailOf, 1310, false, 200, "gone", 0},
		{"delete mid-render", "alice", principal.MethodSession, http.MethodDelete, detailOf, 1313, false, 200, "gone", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			over.reseed(t)
			*queue = fake{queueFails: c.brokerDown, catalogue: kokoro(t, "af_heart", "am_adam")}
			over.caller = principal.Principal{UserID: accounts[c.as], Method: c.method}

			request, err := http.NewRequest(c.verb, server.URL+pathFor(c.path, c.id), nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode != c.status {
				t.Fatalf("status %d, want %d: %s", response.StatusCode, c.status, raw)
			}
			if left := statusOf(c.id); left != c.left {
				t.Errorf("the podcast is left %q, want %q", left, c.left)
			}
			if len(queue.enqueued) != c.drafts {
				t.Errorf("%d drafts queued, want %d", len(queue.enqueued), c.drafts)
			}
			if c.status == http.StatusOK && c.verb != http.MethodDelete {
				var body map[string]any
				if err := json.Unmarshal(raw, &body); err != nil || body["id"] != float64(c.id) || body["status"] != c.left {
					t.Errorf("the answer does not describe the row: %s", raw)
				}
			}
		})
	}

	t.Run("the detail of a row as jsonb keeps it", func(t *testing.T) {
		over.reseed(t)
		over.caller = principal.Principal{UserID: accounts["alice"], Method: principal.MethodSession}
		response, err := server.Client().Get(server.URL + "/api/v1/podcasts/1313")
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(response.Body)
		response.Body.Close()
		// jsonb reordered the keys and kept `1.0`; the answer is in the
		// contract's order with whole numbers, trimmed names, a lowered
		// language and a blank focus as null.
		const want = `{"id":1313,"title":"Numbers as jsonb keeps them","status":"rendering","spec_version":2147483647,` +
			`"spec":{"language":"en-us","style":"conversational","speakers":[` +
			`{"slot":1,"name":"Mara","role":"host","voice_id":"kokoro:af_bella","delivery":null},` +
			`{"slot":2,"name":"Teo","role":"guest","voice_id":"kokoro:am_adam","delivery":""}],` +
			`"duration":{"min_seconds":300,"max_seconds":600},"focus":null},"transcript":null,"has_audio":true,` +
			`"duration_seconds":0,"error":"","created_at":"2026-07-03T00:00:13Z",` +
			`"thread_id":null,"artifact_id":1300}`
		if string(raw) != want {
			t.Errorf("the detail is\n %s\nand should be\n %s", raw, want)
		}
	})

	t.Run("a move is refused once the row has moved", func(t *testing.T) {
		over.reseed(t)
		// 1300 is awaiting_brief. Somebody who decided on `ready` is late.
		var moved bool
		err := Store{Pool: pool}.Write(ctx, func(ctx context.Context, rows Writing) error {
			var err error
			moved, err = rows.Move(ctx, testpostgres.Alice, 1300, statusReady, statusAwaitingBrief)
			return err
		})
		if err != nil || moved || statusOf(1300) != statusAwaitingBrief {
			t.Errorf("moved %v, err %v, left %q", moved, err, statusOf(1300))
		}
		// And somebody who is right about the status but is not the owner
		// moves nothing: the statement itself refuses, not only the route.
		err = Store{Pool: pool}.Write(ctx, func(ctx context.Context, rows Writing) error {
			var err error
			moved, err = rows.Move(ctx, testpostgres.Bob, 1300, statusAwaitingBrief, statusCancelled)
			if err == nil && !moved {
				var deleted bool
				deleted, err = rows.Delete(ctx, testpostgres.Bob, 1300)
				moved = deleted
			}
			return err
		})
		if err != nil || moved || statusOf(1300) != statusAwaitingBrief {
			t.Errorf("another account's statement changed the row: %v, err %v, left %q", moved, err, statusOf(1300))
		}
	})

	t.Run("a delete leaves the notebook output naming nothing", func(t *testing.T) {
		over.reseed(t)
		over.caller = principal.Principal{UserID: accounts["alice"], Method: principal.MethodSession}
		request, _ := http.NewRequest(http.MethodDelete, server.URL+"/api/v1/podcasts/1310", nil)
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		var outputs int
		var named *int64
		if err := pool.QueryRow(ctx,
			"SELECT count(*), max(podcast_id) FROM notebook_outputs WHERE id = 1300").Scan(&outputs, &named); err != nil {
			t.Fatal(err)
		}
		if outputs != 1 || named != nil {
			t.Errorf("%d outputs left, naming %v; the output should survive with no podcast", outputs, named)
		}
	})
}

// sender sends one request as one of the seed's accounts and answers the
// status and the body.
type sender func(as string, method principal.Method, verb, path, body string) (int, map[string]any, []byte)

// TestTheNewRoutesOverARealDatabase is the same question asked of the routes
// that came over with the voice catalogue: do the statements they run -- the
// insert, the guarded replacement, the thread check, the caller's last brief
// -- do what the stand-in says they do, and for their owner alone.
func TestTheNewRoutesOverARealDatabase(t *testing.T) {
	over := openOverPostgres(t)
	ctx := context.Background()
	pool, server, booth, reseed := over.pool, over.server, over.booth, over.reseed

	var do sender = func(as string, method principal.Method, verb, path, body string) (int, map[string]any, []byte) {
		over.caller = principal.Principal{UserID: accounts[as], Method: method}
		request, err := http.NewRequest(verb, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		var decoded map[string]any
		_ = json.Unmarshal(raw, &decoded)
		return response.StatusCode, decoded, raw
	}

	t.Run("a podcast is created at the gate, its maker's, with their last choices", func(t *testing.T) {
		reseed(t)
		// Alice's newest brief decides the language and the voices. Bob's
		// is newer than any of hers and in Spanish (the seed); a statement
		// that forgot the owner would start her podcast from his.
		var language string
		if err := pool.QueryRow(ctx, `SELECT spec->>'language' FROM podcast_runs
			WHERE user_id = $1 AND jsonb_typeof(spec) = 'object'
			ORDER BY created_at DESC, id DESC LIMIT 1`, testpostgres.Alice).Scan(&language); err != nil {
			t.Fatal(err)
		}
		status, body, raw := do("alice", principal.MethodSession, http.MethodPost, "/api/v1/podcasts",
			`{"title": "Made over a real database", "source_content": "Tides.", "thread_id": 1300, "focus": "neaps"}`)
		if status != http.StatusCreated {
			t.Fatalf("status %d: %s", status, raw)
		}
		id := int64(body["id"].(float64))
		var stored struct {
			status, source, focus, owner string
			version                      int32
			thread                       *int64
			speakers                     int
			age                          float64
		}
		err := pool.QueryRow(ctx, `SELECT status::text, source_content, spec->>'focus', user_id::text, spec_version, thread_id,
			jsonb_array_length(spec->'speakers'), extract(epoch FROM now() - created_at)::float8
			FROM podcast_runs WHERE id = $1`, id).Scan(&stored.status, &stored.source, &stored.focus, &stored.owner,
			&stored.version, &stored.thread, &stored.speakers, &stored.age)
		if err != nil {
			t.Fatal(err)
		}
		if stored.status != statusAwaitingBrief || stored.source != "Tides." || stored.focus != "neaps" ||
			stored.owner != testpostgres.Alice ||
			stored.version != 1 || stored.thread == nil || *stored.thread != 1300 || stored.speakers != 2 ||
			stored.age < 0 || stored.age > 60 {
			t.Errorf("stored %+v", stored)
		}
		spec, _ := body["spec"].(map[string]any)
		if spec == nil || body["status"] != statusAwaitingBrief || body["thread_id"] != 1300.0 {
			t.Fatalf("answered %s", raw)
		}
		// The stored tag is whatever jsonb holds; a brief lowers its first part.
		if written, _ := spec["language"].(string); !strings.EqualFold(written, language) || strings.HasPrefix(written, "es") {
			t.Errorf("the brief is in %v; the maker's last brief was in %s", spec["language"], language)
		}
		// And the new podcast is not the other account's to read.
		if status, _, raw := do("bob", principal.MethodSession, http.MethodGet, pathFor("/api/v1/podcasts/%d", id), ""); status != http.StatusNotFound {
			t.Errorf("another account read the new podcast: %d %s", status, raw)
		}
	})

	t.Run("a new podcast's thread is its maker's own", func(t *testing.T) {
		for name, c := range map[string]struct {
			as     string
			method principal.Method
			body   string
			status int
		}{
			"the maker's own thread":        {"alice", principal.MethodSession, `{"title": "t", "source_content": "s", "thread_id": 1300}`, 201},
			"another account's thread":      {"alice", principal.MethodSession, `{"title": "t", "source_content": "s", "thread_id": 1301}`, 404},
			"which is its owner's to use":   {"bob", principal.MethodPersonalAccessToken, `{"title": "t", "source_content": "s", "thread_id": 1301}`, 201},
			"a thread that does not exist":  {"alice", principal.MethodSession, `{"title": "t", "source_content": "s", "thread_id": 1399}`, 404},
			"a body that names a workspace": {"alice", principal.MethodSession, `{"title": "t", "workspace_id": 1300, "source_content": "s"}`, 422},
			"an account with no brief yet":  {"bob", principal.MethodSession, `{"title": "t", "source_content": "s", "speaker_count": 3}`, 201},
		} {
			reseed(t)
			var before, after int
			pool.QueryRow(ctx, "SELECT count(*) FROM podcast_runs").Scan(&before)
			status, _, raw := do(c.as, c.method, http.MethodPost, "/api/v1/podcasts", c.body)
			pool.QueryRow(ctx, "SELECT count(*) FROM podcast_runs").Scan(&after)
			if status != c.status {
				t.Errorf("%s: status %d, want %d: %s", name, status, c.status, raw)
			}
			if made := after - before; (made == 1) != (c.status == http.StatusCreated) {
				t.Errorf("%s: %d podcasts were made", name, made)
			}
		}
	})

	// The chat's and an automation's specialist open a podcast with their
	// own two statements (soloclef_assistant/deliverables, PodcastStore),
	// because that module cannot import this package. This is what holds
	// the two to one idea of a podcast row: what it writes, these routes
	// read, whole, and only for its owner.
	t.Run("a podcast the assistant opened is one these routes read", func(t *testing.T) {
		reseed(t)
		opener := deliverables.Podcasts{
			Rows: deliverables.PodcastStore{Pool: pool}, Voices: kokoro(t, "af_heart", "am_adam"),
			Brief:  deliverables.PodcastBrief{Speakers: 2, MinSeconds: 120, MaxSeconds: 180},
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		}
		if err := opener.Check(); err != nil {
			t.Fatal(err)
		}
		// Bob's newest brief in the seed is Spanish with two voices, so his
		// next podcast starts from those: the last-choices read as well.
		id, err := opener.Open(ctx, deliverables.Owner{UserID: accounts["bob"], ThreadID: 1301},
			deliverables.PodcastRequest{Title: "Tides", Source: "The moon pulls.", Focus: "Why two a day?"})
		if err != nil {
			t.Fatalf("opening: %v", err)
		}
		path := fmt.Sprintf("/api/v1/podcasts/%d", id)
		status, detail, raw := do("bob", principal.MethodSession, http.MethodGet, path, "")
		if status != http.StatusOK {
			t.Fatalf("its owner's read: %d %s", status, raw)
		}
		// The brief the routes could not read would be answered as null.
		const brief = `"spec":{"language":"es","style":"conversational","speakers":[` +
			`{"slot":0,"name":"Host","role":"host","voice_id":"kokoro:ef_dora","delivery":null},` +
			`{"slot":1,"name":"Guest","role":"guest","voice_id":"kokoro:em_alex","delivery":null}],` +
			`"duration":{"min_seconds":120,"max_seconds":180},"focus":"Why two a day?"}`
		if detail["title"] != "Tides" || detail["status"] != "awaiting_brief" || detail["spec_version"] != float64(1) ||
			detail["thread_id"] != float64(1301) || detail["has_audio"] != false || !strings.Contains(string(raw), brief) {
			t.Errorf("the detail is %s", raw)
		}
		if status, _, raw := do("alice", principal.MethodSession, http.MethodGet, path, ""); status != http.StatusNotFound {
			t.Errorf("another account read it: %d %s", status, raw)
		}
		// Nobody opens one on a thread that is not theirs.
		if _, err := opener.Open(ctx, deliverables.Owner{UserID: accounts["alice"], ThreadID: 1301},
			deliverables.PodcastRequest{Title: "Not hers", Source: "S."}); err == nil {
			t.Error("a podcast was opened on another account's thread")
		}
	})

	const edited = `{"language": "en", "style": "interview", "speakers": [` +
		`{"slot": 0, "name": "Mara", "role": "host", "voice_id": "kokoro:af_bella"}, ` +
		`{"slot": 1, "name": "Teodor", "role": "expert", "voice_id": "kokoro:am_adam"}], ` +
		`"duration": {"min_seconds": 120, "max_seconds": 180}, "focus": null}`
	briefOf := func(id int64) (version int32, name string, delivery *string) {
		err := pool.QueryRow(ctx, `SELECT spec_version, spec->'speakers'->1->>'name', spec->'speakers'->1->>'delivery'
			FROM podcast_runs WHERE id = $1`, id).Scan(&version, &name, &delivery)
		if err != nil {
			t.Fatal(err)
		}
		return version, name, delivery
	}

	t.Run("a brief is replaced, and keeps the note casting wrote", func(t *testing.T) {
		reseed(t)
		status, body, raw := do("alice", principal.MethodSession, http.MethodPatch, "/api/v1/podcasts/1300/spec",
			`{"spec": `+edited+`, "expected_version": 1}`)
		if status != http.StatusOK || body["spec_version"] != 2.0 {
			t.Fatalf("status %d: %s", status, raw)
		}
		version, name, delivery := briefOf(1300)
		if version != 2 || name != "Teodor" || delivery == nil || *delivery != "dry, unhurried" {
			t.Errorf("stored version %d, name %q, delivery %v", version, name, delivery)
		}
	})

	t.Run("a brief is not replaced by a stale edit, another account, or off the gate", func(t *testing.T) {
		for name, c := range map[string]struct {
			as      string
			id      int64
			version string
			status  int
		}{
			"a version already left behind": {"alice", 1300, "7", 409},
			"another account":               {"bob", 1300, "1", 404},
			"a finished episode":            {"alice", 1301, "4", 409},
		} {
			reseed(t)
			before, _, _ := briefOf(c.id)
			status, _, raw := do(c.as, principal.MethodSession, http.MethodPatch, pathFor("/api/v1/podcasts/%d/spec", c.id),
				`{"spec": `+edited+`, "expected_version": `+c.version+`}`)
			if after, speaker, _ := briefOf(c.id); status != c.status || after != before || speaker == "Teodor" {
				t.Errorf("%s: status %d (want %d), version %d to %d: %s", name, status, c.status, before, after, raw)
			}
		}
	})

	t.Run("the replacement itself refuses a brief that has moved", func(t *testing.T) {
		reseed(t)
		for name, c := range map[string]struct {
			as      string
			id      int64
			version int64
			wins    bool
		}{
			"the version it was checked against":  {testpostgres.Alice, 1300, 1, true},
			"a version another edit has replaced": {testpostgres.Alice, 1300, 2, false},
			"a podcast that has left the gate":    {testpostgres.Alice, 1302, 1, false},
			"a podcast that is not there":         {testpostgres.Alice, 1399, 1, false},
			// The statement itself, with everything else right.
			"another account's podcast": {testpostgres.Bob, 1300, 1, false},
		} {
			reseed(t)
			var replaced bool
			err := Store{Pool: pool}.Write(ctx, func(ctx context.Context, rows Writing) error {
				var err error
				replaced, err = rows.ReplaceBrief(ctx, c.as, c.id, c.version, []byte(edited))
				return err
			})
			if err != nil || replaced != c.wins {
				t.Errorf("%s: replaced %v, err %v", name, replaced, err)
			}
		}
	})

	t.Run("a sample is for anyone signed in", func(t *testing.T) {
		reseed(t)
		for _, as := range []string{"alice", "bob"} {
			booth.sampled = nil
			status, _, raw := do(as, principal.MethodSession, http.MethodGet, "/api/v1/podcasts/voices/kokoro:af_heart/preview", "")
			if status != http.StatusOK || len(booth.sampled) != 1 || string(raw) != string(sampleAudio) {
				t.Errorf("%s: status %d, the booth asked %d times, %d bytes", as, status, len(booth.sampled), len(raw))
			}
		}
	})
}

func pathFor(pattern string, id int64) string { return fmt.Sprintf(pattern, id) }
