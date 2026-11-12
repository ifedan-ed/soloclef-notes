package publicchat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/corsmirror"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// recordedTable is testdata/publicchat_python.json, recorded from the Python
// version before it was retired: one case per request made to the real ASGI
// app, holding the rows the stub session was given, and then
// everything the Python route did with them -- the status, the headers that
// decide the wire bytes, the body as Starlette wrote it, and the statements
// it issued in order.
type recordedTable struct {
	RequestID string             `json:"request_id"`
	Origin    string             `json:"origin"`
	Now       string             `json:"now"`
	Expiries  map[string]*string `json:"expiries"`
	Mounted   []string           `json:"mounted"`
	Cases     []recordedCase     `json:"cases"`
}

type recordedCase struct {
	Name  string `json:"name"`
	Route string `json:"route"`
	Path  string `json:"path"`
	Query string `json:"query"`

	Snapshot *struct {
		OutputID *int64 `json:"notebook_output_id"`
		Expires  string `json:"expires"`
		Data     string `json:"data"`
	} `json:"snapshot"`
	Artifacts []struct {
		ID         int64   `json:"id"`
		Format     string  `json:"format"`
		Generation int64   `json:"generation"`
		Title      *string `json:"title"`
		Metadata   *string `json:"metadata"`
	} `json:"artifacts"`
	Files []struct {
		ID         int64  `json:"id"`
		ArtifactID int64  `json:"artifact_id"`
		Role       string `json:"role"`
		Filename   string `json:"filename"`
		MimeType   string `json:"mime_type"`
		Size       int64  `json:"size_bytes"`
	} `json:"files"`
	DriverEmulated bool `json:"driver_emulated"`

	Status        int     `json:"status"`
	ContentType   *string `json:"content_type"`
	ContentLength *string `json:"content_length"`
	Location      *string `json:"location"`
	Body          string  `json:"body"`
	// CORS is every value of each of the four headers Starlette's CORS
	// middleware writes, empty where the answer carried none.
	CORS       map[string][]string `json:"cors"`
	Statements []struct {
		Statement  string                     `json:"statement"`
		Parameters map[string]json.RawMessage `json:"parameters"`
	} `json:"statements"`
}

// world is the stand-in for the database, for one case at a time. It answers
// what the script's stub session answered and writes down what it was asked,
// in the script's own names, so the two orders can be compared.
type world struct {
	current  recordedCase
	expiries map[string]*time.Time
	now      time.Time
	// answered is the moment the envelope's timestamp is written with.
	answered time.Time
	// asked is the statements this tier issued, in order, each with what it
	// carried.
	asked []string
}

// errDriver stands in for the database refusing a parameter: an integer that
// does not fit the column, or a NUL byte in text. asyncpg refuses to encode
// the first and Postgres refuses the second from either driver; the cases
// marked `driver_emulated` are about *which* statement that is.
var errDriver = errors.New("the database refuses this parameter")

func fitsTheColumn(values ...int64) bool {
	for _, value := range values {
		if value < math.MinInt32 || value > math.MaxInt32 {
			return false
		}
	}
	return true
}

func (w *world) SnapshotByToken(_ context.Context, token string) (*Snapshot, error) {
	w.asked = append(w.asked, "snapshot("+token+")")
	if strings.ContainsRune(token, 0) {
		return nil, errDriver
	}
	held := w.current.Snapshot
	if held == nil {
		return nil, nil
	}
	return &Snapshot{
		OutputID:  held.OutputID,
		ExpiresAt: w.expiries[held.Expires],
		Data:      []byte(held.Data),
	}, nil
}

func (w *world) VideoArtifact(_ context.Context, artifactID int64) (*VideoArtifact, error) {
	w.asked = append(w.asked, fmt.Sprintf("video_artifact(%d)", artifactID))
	if !fitsTheColumn(artifactID) {
		return nil, errDriver
	}
	for _, row := range w.current.Artifacts {
		if row.ID == artifactID && row.Format == "video" {
			found := &VideoArtifact{ID: row.ID, Title: row.Title}
			if row.Metadata != nil {
				found.Metadata = []byte(*row.Metadata)
			}
			return found, nil
		}
	}
	return nil, nil
}

func (w *world) ArtifactGeneration(_ context.Context, artifactID int64) (int64, bool, error) {
	w.asked = append(w.asked, fmt.Sprintf("artifact(%d)", artifactID))
	if !fitsTheColumn(artifactID) {
		return 0, false, errDriver
	}
	for _, row := range w.current.Artifacts {
		if row.ID == artifactID {
			return row.Generation, true, nil
		}
	}
	return 0, false, nil
}

func (w *world) ArtifactFiles(_ context.Context, artifactID int64) ([]StoredFile, error) {
	w.asked = append(w.asked, fmt.Sprintf("artifact_files(%d)", artifactID))
	if !fitsTheColumn(artifactID) {
		return nil, errDriver
	}
	var files []StoredFile
	for _, row := range w.current.Files {
		if row.ArtifactID == artifactID {
			files = append(files, StoredFile{
				ID: row.ID, Role: row.Role, Filename: row.Filename,
				MimeType: row.MimeType, Size: row.Size,
			})
		}
	}
	return files, nil
}

func (w *world) ArtifactFormats(_ context.Context, artifactIDs []int64) (map[int64]string, error) {
	w.asked = append(w.asked, "artifact_formats("+joined(artifactIDs)+")")
	if !fitsTheColumn(artifactIDs...) {
		return nil, errDriver
	}
	formats := map[int64]string{}
	for _, row := range w.current.Artifacts {
		for _, id := range artifactIDs {
			if row.ID == id {
				formats[row.ID] = row.Format
			}
		}
	}
	return formats, nil
}

func joined(ids []int64) string {
	ordered := append([]int64{}, ids...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	texts := make([]string, 0, len(ordered))
	for _, id := range ordered {
		texts = append(texts, strconv.FormatInt(id, 10))
	}
	return strings.Join(texts, ",")
}

// served is the three routes as cmd/api is to mount them: each on a ServeMux
// pattern of its own -- there is no gate in front of them, because they have
// no caller -- then on Huma, behind the request-id middleware and the real
// CORS policy, so that a route's corsmirror.Suppress is observable as headers
// that are not sent.
func served(w *world, origin string) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	envelope := errorenvelope.Writer{
		ReportURL: "https://notes.example/contact",
		// The envelope's timestamp is the one thing in a refusal that is a
		// clock reading, so each case is answered at the moment the Python
		// tier answered it and the bodies can be compared whole.
		Now: func() time.Time { return w.answered },
	}
	now := func() time.Time { return w.now }

	inner := http.NewServeMux()
	api := humago.New(inner, huma.DefaultConfig("test", "0.0.1"))
	PageRoute{Rows: w, Errors: envelope, Logger: logger, Now: now}.Register(api)
	VideoRoute{Rows: w, Errors: envelope, Logger: logger, Now: now}.Register(api)
	PodcastRoute{Rows: w, Errors: envelope, Logger: logger, Now: now}.Register(api)

	routes := http.NewServeMux()
	for _, path := range []string{PagePath, VideoPath, PodcastPath} {
		routes.Handle(http.MethodGet+" "+path, api.Adapter())
	}
	cors := corsmirror.NewPolicy([]string{origin})
	return requestcontext.Middleware(logger, cors.Middleware(routes))
}

// answer is one response of this tier, read whole.
type answer struct {
	status int
	header http.Header
	body   string
}

func ask(t *testing.T, server *httptest.Server, table recordedTable, c recordedCase) answer {
	t.Helper()
	target := server.URL + c.Path
	if c.Query != "" {
		target += "?" + c.Query
	}
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("%s: %v", c.Name, err)
	}
	request.Header.Set("X-Request-ID", table.RequestID)
	request.Header.Set("Origin", table.Origin)
	// Never follow a redirect: what this tier's router answers for a path it
	// would rather be spelled differently is itself the thing to look at.
	client := *server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s: %v", c.Name, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("%s: reading the body: %v", c.Name, err)
	}
	return answer{status: response.StatusCode, header: response.Header, body: string(body)}
}

// differences is everything about an answer that is not what Python sent:
// the status, the body byte for byte, the two headers that decide what a
// client reads, and the four CORS headers.
func differences(c recordedCase, got answer) []string {
	var found []string
	if got.status != c.Status {
		return []string{fmt.Sprintf("status %d, Python answered %d\n Go     %s\n Python %s",
			got.status, c.Status, got.body, c.Body)}
	}
	if got.body != c.Body {
		found = append(found, fmt.Sprintf("the body differs\n Go     %s\n Python %s", got.body, c.Body))
	}
	for header, recorded := range map[string]*string{
		"Content-Type":   c.ContentType,
		"Content-Length": c.ContentLength,
	} {
		sent, present := got.header[header]
		switch {
		case recorded == nil && present:
			found = append(found, fmt.Sprintf("this tier sent %s %q and Python sent none", header, sent))
		case recorded != nil && (!present || len(sent) != 1 || sent[0] != *recorded):
			found = append(found, fmt.Sprintf("this tier sent %s %q and Python sent %q", header, sent, *recorded))
		}
	}
	// The CORS headers, which is where a 500 nobody caught differs from every
	// other answer: Starlette writes it from outside its CORS middleware, so
	// it carries none of the four.
	for header, recorded := range c.CORS {
		if sent := got.header.Values(header); !reflect.DeepEqual(append([]string{}, sent...), append([]string{}, recorded...)) {
			found = append(found, fmt.Sprintf("this tier sent %s %q and Python sent %q", header, sent, recorded))
		}
	}
	return found
}

// answeredByTheRouter is a case the Python *router* answered: no statement
// was issued, and the body is Starlette's bare 404 or a redirect. The route
// never ran, so there is nothing of a route to compare.
func answeredByTheRouter(c recordedCase) bool {
	return len(c.Statements) == 0 && (c.Body == routerNotFound || c.Status == http.StatusTemporaryRedirect)
}

// TestPythonsOwnAnswers replays every answer the three Python routes gave.
//
// Each case is sent, as a real HTTP request to a real server, to this
// package's own routes, and five things are held to the recording: the
// status, the content type and length, the body byte for byte, the CORS
// headers, and the statements issued with what each carried. The order of
// the statements is how the order of the refusals is proved rather than
// asserted: an artifact the link does not share must be refused after one
// statement, and one that is not a video after two.
//
// The cases the Python router answered itself are replayed too and handled
// apart, below, because there this tier's *router* is what is being compared
// and it is not this package's.
func TestPythonsOwnAnswers(t *testing.T) {
	table := readTable(t)
	state := &world{}
	server := httptest.NewServer(served(state, table.Origin))
	defer server.Close()

	replayed, unported := 0, 0
	routerAgreed, routerDiffered := 0, 0
	for _, c := range table.Cases {
		if c.Route == "presentation" {
			// GET .../video-presentations/{id}: recorded, not ported.
			unported++
			continue
		}
		*state = world{
			current:  c,
			expiries: expiries(t, table),
			now:      moment(t, table.Now),
			answered: answeredAt(t, c),
		}
		got := ask(t, server, table, c)

		if answeredByTheRouter(c) {
			// A path Starlette's router did not match, or redirected. What
			// answers it here is Go's ServeMux, in front of this package.
			// A parameter holding an encoded "/" is the one shape ServeMux
			// lets through to a handler, and the handler answers it as the
			// Python router did (unroutable): that is held to the recording
			// like any other case. The rest never reach a handler on either
			// tier; they are written down rather than skipped, so that a
			// change in either router shows up in this test's output.
			found := differences(c, got)
			switch {
			case len(found) == 0:
				routerAgreed++
				replayed++
			case strings.Contains(c.Path, "%2F"):
				t.Errorf("%s: the handler runs for this path and must answer as Starlette's router does: %s",
					c.Name, strings.Join(found, "; "))
			default:
				routerDiffered++
				t.Logf("%s: the Python router answered %d and this tier's router %d, with no route run on either",
					c.Name, c.Status, got.status)
			}
			if len(state.asked) != 0 {
				t.Errorf("%s: the Python route never ran, and this tier issued %v", c.Name, state.asked)
			}
			continue
		}

		for _, difference := range differences(c, got) {
			t.Errorf("%s: %s", c.Name, difference)
		}
		if want := statementsToExpect(c); !reflect.DeepEqual(append([]string{}, state.asked...), want) {
			t.Errorf("%s: this tier issued %q and Python issued %q", c.Name, state.asked, want)
		}
		replayed++
	}

	if replayed == 0 {
		t.Fatal("no recorded answer was replayed, so this proves nothing")
	}
	t.Logf("replayed %d recorded answers byte for byte (%d of them the router's own); "+
		"%d more were answered by each tier's router differently, with no route run; "+
		"%d belong to a route this package does not serve",
		replayed, routerAgreed, routerDiffered, unported)
}

// statementsToExpect is the statements the Python route issued, as this
// tier's world spells them. Two kinds are left out, because the Python tier
// *sends* a statement there that this tier has no way to send and answers
// the same failure without:
//
//   - `artifacts.id = true`, which Postgres refuses when it parses it
//     (output.go);
//   - an IN list SQLAlchemy typed BOOLEAN because a bool came first in it,
//     which either SQLAlchemy or Postgres then refuses (upcast.go:askable);
//   - a statement carrying an integer past int64, which asyncpg refuses to
//     encode and Go cannot hold in a parameter at all.
//
// Each is the last statement of its case, and each case is a 500.
func statementsToExpect(c recordedCase) []string {
	want := []string{}
	for _, statement := range c.Statements {
		switch statement.Statement {
		case "snapshot":
			var token string
			_ = json.Unmarshal(statement.Parameters["share_token_1"], &token)
			want = append(want, "snapshot("+token+")")
		case "video_artifact", "artifact":
			id, err := strconv.ParseInt(string(statement.Parameters["id_1"]), 10, 64)
			if err != nil {
				return want
			}
			want = append(want, fmt.Sprintf("%s(%d)", statement.Statement, id))
		case "artifact_files":
			want = append(want, "artifact_files("+string(statement.Parameters["artifact_id_1"])+")")
		case "artifact_formats":
			var ids []int64
			if err := json.Unmarshal(statement.Parameters["ids"], &ids); err != nil {
				return want
			}
			if string(statement.Parameters["first_is_a_bool"]) == "true" {
				return want
			}
			want = append(want, "artifact_formats("+joined(ids)+")")
		case "artifact_compared_with_true", "artifact_compared_with_false":
			return want
		}
	}
	return want
}

// answeredAt is the moment the Python tier wrote a refusal's envelope, read
// back out of the recorded body. A success has no clock reading in it, and
// neither has the router's bare 404.
func answeredAt(t *testing.T, c recordedCase) time.Time {
	t.Helper()
	if c.Status < 400 || c.Body == routerNotFound {
		return time.Time{}
	}
	var envelope struct {
		Error struct {
			Timestamp string `json:"timestamp"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(c.Body), &envelope); err != nil {
		t.Fatalf("%s: the recorded refusal is not an envelope: %v", c.Name, err)
	}
	return moment(t, envelope.Error.Timestamp)
}

func moment(t *testing.T, text string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		t.Fatalf("the recorded moment %q cannot be read: %v", text, err)
	}
	return parsed
}

func expiries(t *testing.T, table recordedTable) map[string]*time.Time {
	t.Helper()
	known := map[string]*time.Time{}
	for name, text := range table.Expiries {
		if text == nil {
			known[name] = nil
			continue
		}
		parsed := moment(t, *text)
		known[name] = &parsed
	}
	return known
}

func readTable(t *testing.T) recordedTable {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "publicchat_python.json"))
	if err != nil {
		t.Fatalf("reading the recorded answers: %v", err)
	}
	var table recordedTable
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatalf("parsing the recorded answers: %v", err)
	}
	if len(table.Cases) == 0 {
		t.Fatal("the recorded table is empty, so it proves nothing")
	}
	return table
}
