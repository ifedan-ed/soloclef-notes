package podcasts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/corsmirror"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/voices"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// A brief and a transcript as the app stores them. Every name and line is
// invented.
const (
	goodBrief = `{"language": "en-US", "style": "conversational", "speakers": [` +
		`{"slot": 0, "name": "Mara", "role": "host", "voice_id": "kokoro:af_bella", "delivery": null}, ` +
		`{"slot": 1, "name": "Teo", "role": "guest", "voice_id": "kokoro:am_adam", "delivery": "dry"}], ` +
		`"duration": {"min_seconds": 300, "max_seconds": 600}, "focus": "the tide tables"}`
	goodTranscript = `{"turns": [{"speaker": 0, "text": "Welcome back."}, {"speaker": 1, "text": "Glad to be here."}]}`
	// Half a brief: no duration.
	brokenBrief = `{"language": "en", "speakers": []}`
)

const (
	episode  = int64(55) // an artifact id
	callerID = "11111111-2222-4333-8444-555555555555"
	// strangerID is a second account. What it owns the caller must never
	// see, and what the caller owns it must never see.
	strangerID = "99999999-2222-4333-8444-555555555555"
)

// fake is the database, the queue, the voice catalogue and the sample booth
// for one test: podcasts by id and whose each is, and a broker that can be
// down.
//
// Every statement is handed the account it is run for, as the real ones
// are, and finds only that account's rows. So a route that passed anything
// but the signed-in caller, or forgot the owner on a write, fails here.
type fake struct {
	// asker is the account the requests are made as: callerID unless a test
	// says otherwise.
	asker    string
	podcasts map[int64]PodcastOfOwnerRow
	// owners is whose each podcast is. One not named is callerID's.
	owners map[int64]string
	// threads is whose each chat thread is.
	threads map[int64]string
	// sources is the text each created podcast was given, by id: the one
	// column no answer carries.
	sources map[int64]string
	// meanwhile runs once, after the gate and before a write's statement: it
	// is another request getting there first.
	meanwhile func(f *fake)
	// committedByAnother is the rows as that other request left them.
	committedByAnother map[int64]PodcastOfOwnerRow
	queueFails         bool
	enqueued           []produce.Message
	// moves counts the statements that changed a status.
	moves int

	// catalogue is the voices on offer; newFake gives every test Kokoro's,
	// with two hosts configured. catalogueErr, when set, is the catalogue
	// not answering.
	catalogue    Catalogue
	catalogueErr error
	// sampled is every voice a sample was asked for, and sampleErr is the
	// booth failing. No test synthesises anything.
	sampled   []string
	sampleErr error
}

// sampleAudio is what the booth answers: bytes that start as an MP3 does.
var sampleAudio = []byte("ID3\x04\x00 not really audio")

func (f *fake) Roster(ctx context.Context) (*voices.Roster, error) {
	if f.catalogueErr != nil {
		return nil, f.catalogueErr
	}
	return f.catalogue.Roster(ctx)
}

func (f *fake) Of(_ context.Context, voice voices.Voice) (voices.Sample, error) {
	f.sampled = append(f.sampled, voice.ID)
	if f.sampleErr != nil {
		return voices.Sample{}, f.sampleErr
	}
	return voices.Sample{Audio: sampleAudio, ContentType: "audio/mpeg"}, nil
}

// kokoro is the catalogue of a deployment whose speech service is a hosted
// Kokoro, with the given hosts. Kokoro's roster is written down, so nothing
// is asked of a proxy and none is given.
func kokoro(t *testing.T, hosts ...string) Catalogue {
	t.Helper()
	catalogue, err := voices.New(voices.Settings{
		Service: "openai/hosted-kokoro-82m", APIBase: "http://proxy.example/v1", PodcastVoices: hosts,
	}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return catalogue
}

// modelSpeaks is a proxy that says the model has these voices.
type modelSpeaks []string

func (m modelSpeaks) ModelVoices(context.Context, string) ([]string, error) { return m, nil }

// openAI is the catalogue of a deployment whose speech model has the given
// voices, each of which speaks any language.
func openAI(t *testing.T, speaks ...string) Catalogue {
	t.Helper()
	catalogue, err := voices.New(voices.Settings{
		Service: "openai/hosted-tts", APIBase: "http://proxy.example/v1",
	}, modelSpeaks(speaks), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return catalogue
}

func (f *fake) Read(ctx context.Context, work func(context.Context, Reading) error) error {
	return work(ctx, f)
}

// Write is one transaction: what the work changed is kept when it returns
// nil and thrown away when it returns anything else. What another request
// committed in the meantime (meanwhile) is not this transaction's to throw
// away, and stays.
func (f *fake) Write(ctx context.Context, work func(context.Context, Writing) error) error {
	before := maps.Clone(f.podcasts)
	f.committedByAnother = nil
	if err := work(ctx, f); err != nil {
		f.podcasts = before
		if f.committedByAnother != nil {
			f.podcasts = f.committedByAnother
		}
		return err
	}
	return nil
}

// ownerOf is whose a podcast is.
func (f *fake) ownerOf(id int64) string {
	if owner, named := f.owners[id]; named {
		return owner
	}
	return callerID
}

// owned is the row, if there is one and it is this account's: the
// `WHERE id = $1 AND user_id = $2` of every statement.
func (f *fake) owned(userID string, id int64) (PodcastOfOwnerRow, bool) {
	row, found := f.podcasts[id]
	return row, found && f.ownerOf(id) == userID
}

func (f *fake) Podcast(_ context.Context, userID string, id int64) (*Stored, error) {
	row, found := f.owned(userID, id)
	if !found {
		return nil, nil
	}
	return newStored(row)
}

func (f *fake) interleave() {
	if f.meanwhile != nil {
		other := f.meanwhile
		f.meanwhile = nil
		other(f)
		f.committedByAnother = maps.Clone(f.podcasts)
	}
}

func (f *fake) Move(_ context.Context, userID string, id int64, current, target string) (bool, error) {
	f.interleave()
	row, found := f.owned(userID, id)
	if !found || row.Status != current {
		return false, nil
	}
	row.Status = target
	f.podcasts[id] = row
	f.moves++
	return true, nil
}

// LatestBrief is the account's newest podcast that holds a JSON object for
// a brief, as the statement asks.
func (f *fake) LatestBrief(_ context.Context, userID string) (any, error) {
	var latest *PodcastOfOwnerRow
	for id, row := range f.podcasts {
		if f.ownerOf(id) != userID || !strings.HasPrefix(strings.TrimSpace(string(row.Spec)), "{") {
			continue
		}
		if latest == nil || row.CreatedAt.After(latest.CreatedAt) ||
			(row.CreatedAt.Equal(latest.CreatedAt) && row.ID > latest.ID) {
			latest = &row
		}
	}
	if latest == nil {
		return nil, nil
	}
	return decodeColumn(latest.Spec)
}

func (f *fake) OwnsThread(_ context.Context, userID string, threadID int64) (bool, error) {
	owner, found := f.threads[threadID]
	return found && owner == userID, nil
}

// createdAt is when the fake says a podcast was made.
var createdAt = time.Date(2026, 9, 8, 7, 6, 5, 0, time.UTC)

func (f *fake) Create(_ context.Context, userID string, podcast NewPodcast) (*Stored, error) {
	f.interleave()
	id := int64(100)
	for taken := range f.podcasts {
		if taken >= id {
			id = taken + 1
		}
	}
	row := PodcastOfOwnerRow{
		ID: id, Title: podcast.Title, Status: statusAwaitingBrief, SpecVersion: 1,
		Spec: podcast.Spec, ThreadID: podcast.ThreadID,
		CreatedAt: createdAt,
	}
	f.podcasts[id] = row
	f.owners[id] = userID
	f.sources[id] = podcast.SourceContent
	return newStored(row)
}

func (f *fake) ReplaceBrief(_ context.Context, userID string, id, expectedVersion int64, spec []byte) (bool, error) {
	f.interleave()
	row, found := f.owned(userID, id)
	if !found || row.Status != statusAwaitingBrief || row.SpecVersion != expectedVersion {
		return false, nil
	}
	row.Spec, row.SpecVersion = spec, row.SpecVersion+1
	f.podcasts[id] = row
	return true, nil
}

func (f *fake) Delete(_ context.Context, userID string, id int64) (bool, error) {
	f.interleave()
	_, found := f.owned(userID, id)
	if found {
		delete(f.podcasts, id)
	}
	return found, nil
}

func (f *fake) Enqueue(_ context.Context, message produce.Message) (string, error) {
	if f.queueFails {
		return "", errors.New("the broker is unreachable")
	}
	f.enqueued = append(f.enqueued, message)
	return "task-id", nil
}

func text(value string) []byte { return []byte(value) }

func pointer[T any](value T) *T { return &value }

// podcast is one of the caller's rows, waiting at the brief gate with a
// brief, changed by whatever the test needs changed.
func podcast(id int64, change func(*PodcastOfOwnerRow)) PodcastOfOwnerRow {
	row := PodcastOfOwnerRow{
		ID: id, Title: "The harbour hour", Status: statusAwaitingBrief, SpecVersion: 1,
		Spec:      text(goodBrief),
		CreatedAt: time.Date(2026, 3, 4, 5, 6, 7, 250_000_000, time.UTC),
	}
	if change != nil {
		change(&row)
	}
	return row
}

// lounge is one of the caller's chat threads; hallway is another account's.
const (
	lounge  = int64(12)
	hallway = int64(13)
)

func newFake(rows ...PodcastOfOwnerRow) *fake {
	f := &fake{
		asker:    callerID,
		podcasts: map[int64]PodcastOfOwnerRow{},
		owners:   map[int64]string{},
		threads:  map[int64]string{lounge: callerID, hallway: strangerID},
		sources:  map[int64]string{},
	}
	for _, row := range rows {
		f.podcasts[row.ID] = row
	}
	return f
}

// register is the eleven routes as cmd/api is to register them. Registering
// them on one mux is itself a test: ServeMux panics on two patterns that
// could both match a request, and `/podcasts/voices` beside
// `/podcasts/{podcast_id}` is exactly the kind of pair it checks.
func register(api huma.API, podcasts Database, queue interface {
	Enqueue(context.Context, produce.Message) (string, error)
}, catalogue Catalogue, samples Sampler, envelope errorenvelope.Writer, logger *slog.Logger) {
	VoicesRoute{Voices: catalogue, Errors: envelope, Logger: logger}.Register(api)
	LanguagesRoute{Voices: catalogue, Errors: envelope, Logger: logger}.Register(api)
	PreviewRoute{Voices: catalogue, Samples: samples, Errors: envelope, Logger: logger}.Register(api)
	CreateRoute{Podcasts: podcasts, Voices: catalogue, Errors: envelope, Logger: logger}.Register(api)
	DetailRoute{Podcasts: podcasts, Errors: envelope, Logger: logger}.Register(api)
	BriefRoute{Podcasts: podcasts, Voices: catalogue, Errors: envelope, Logger: logger}.Register(api)
	NewApproveRoute(podcasts, queue, envelope, logger).Register(api)
	NewRegenerateRoute(podcasts, envelope, logger).Register(api)
	NewRevertRoute(podcasts, envelope, logger).Register(api)
	NewCancelRoute(podcasts, envelope, logger).Register(api)
	DeleteRoute{Podcasts: podcasts, Errors: envelope, Logger: logger}.Register(api)
}

const origin = "https://notes.example"

// answered is what one request came back with.
type answered struct {
	status int
	header http.Header
	// raw is the body as sent; body is the same when it is a JSON object,
	// and list when it is a JSON array.
	raw  []byte
	body map[string]any
	list []any
}

// message is the envelope's sentence, for a refusal.
func (a answered) message() string {
	envelope, _ := a.body["error"].(map[string]any)
	sentence, _ := envelope["message"].(string)
	return sentence
}

// faults is where a 422 says the body is wrong: each failure's location,
// joined with dots, and what it says there.
func (a answered) faults() map[string]string {
	envelope, _ := a.body["error"].(map[string]any)
	fields, _ := envelope["fields"].([]any)
	found := map[string]string{}
	for _, entry := range fields {
		field, _ := entry.(map[string]any)
		parts := []string{}
		for _, part := range field["loc"].([]any) {
			parts = append(parts, part.(string))
		}
		found[strings.Join(parts, ".")], _ = field["msg"].(string)
	}
	return found
}

// nobody is a request that reached a route with no caller, which the tier's
// authentication never lets happen.
const nobody = principal.Method("")

// ask sends one request with no body.
func ask(t *testing.T, f *fake, method principal.Method, verb, path string) answered {
	t.Helper()
	return send(t, f, method, verb, path, "")
}

// send sends one request to the eleven routes, mounted behind the
// request-id, CORS and body-reading middlewares as cmd/api mounts them, as a
// caller who signed in the given way. A body is sent as JSON.
func send(t *testing.T, f *fake, method principal.Method, verb, path, body string) answered {
	t.Helper()
	if f.catalogue == nil {
		f.catalogue = kokoro(t, "af_heart", "am_adam")
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "0.0.1"))
	register(api, f, f, f, f, errorenvelope.Writer{ReportURL: origin + "/contact"}, logger)
	withCaller := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if method != nobody {
			request = request.WithContext(principal.WithPrincipal(request.Context(),
				principal.Principal{UserID: f.asker, Method: method}))
		}
		mux.ServeHTTP(response, request)
	})
	// The body reader sits outside the authentication, as it does in
	// cmd/api. A body it would hand to the Python tier is one no test here
	// means to send.
	bodies := requestbody.Middleware{
		Covers: func(*http.Request) bool { return true },
	}
	server := httptest.NewServer(requestcontext.Middleware(logger,
		corsmirror.NewPolicy([]string{origin}).Middleware(bodies.Wrap(withCaller))))
	defer server.Close()

	var sent io.Reader
	if body != "" {
		sent = strings.NewReader(body)
	}
	request, err := http.NewRequest(verb, server.URL+path, sent)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Request-ID", "req_test")
	request.Header.Set("Origin", origin)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	got := answered{status: response.StatusCode, header: response.Header, raw: raw}
	if json.Unmarshal(raw, &got.body) != nil {
		_ = json.Unmarshal(raw, &got.list)
	}
	return got
}

func TestTheDetailCarriesEverythingTheWebAppParses(t *testing.T) {
	f := newFake(podcast(41, func(row *PodcastOfOwnerRow) {
		row.Status = statusReady
		row.SpecVersion = 3
		row.PodcastTranscript = text(goodTranscript)
		row.DurationSeconds = pointer(int64(412))
		row.ArtifactID = pointer(episode)
		row.ThreadID = pointer(int64(12))
	}))
	got := ask(t, f, principal.MethodSession, http.MethodGet, "/api/v1/podcasts/41")
	if got.status != http.StatusOK {
		t.Fatalf("status %d: %v", got.status, got.body)
	}
	want := map[string]any{
		"id": 41.0, "title": "The harbour hour", "status": "ready", "spec_version": 3.0,
		"spec": map[string]any{
			"language": "en-US", "style": "conversational",
			"speakers": []any{
				map[string]any{"slot": 0.0, "name": "Mara", "role": "host", "voice_id": "kokoro:af_bella", "delivery": nil},
				map[string]any{"slot": 1.0, "name": "Teo", "role": "guest", "voice_id": "kokoro:am_adam", "delivery": "dry"},
			},
			"duration": map[string]any{"min_seconds": 300.0, "max_seconds": 600.0},
			"focus":    "the tide tables",
		},
		"transcript": map[string]any{"turns": []any{
			map[string]any{"speaker": 0.0, "text": "Welcome back."},
			map[string]any{"speaker": 1.0, "text": "Glad to be here."},
		}},
		"has_audio": true, "duration_seconds": 412.0, "error": nil,
		"created_at": "2026-03-04T05:06:07.250000Z",
		"thread_id":  12.0, "artifact_id": 55.0,
	}
	if !reflect.DeepEqual(got.body, want) {
		t.Errorf("the detail is\n %v\nand should be\n %v", got.body, want)
	}
	if got.header.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type %q", got.header.Get("Content-Type"))
	}
}

func TestAPodcastWithNothingYetIsAnsweredWithNulls(t *testing.T) {
	f := newFake(podcast(41, func(row *PodcastOfOwnerRow) {
		row.Status, row.Spec = statusPending, nil
	}))
	got := ask(t, f, principal.MethodSession, http.MethodGet, "/api/v1/podcasts/41")
	for _, field := range []string{"spec", "transcript", "duration_seconds", "error", "thread_id", "artifact_id"} {
		if value, present := got.body[field]; !present || value != nil {
			t.Errorf("%s is %v, and should be present and null", field, value)
		}
	}
	if got.body["has_audio"] != false {
		t.Errorf("has_audio is %v for a podcast with no episode", got.body["has_audio"])
	}
}

// A column that cannot be read costs the caller that column, not the podcast.
func TestAColumnThatCannotBeReadIsNullAndTheRestIsStillAnswered(t *testing.T) {
	f := newFake(podcast(41, func(row *PodcastOfOwnerRow) {
		row.Status = statusFailed
		row.Spec = text(brokenBrief)
		row.PodcastTranscript = text(`{"turns": "none"}`)
		row.Error = pointer("the speech provider answered 503")
	}))
	got := ask(t, f, principal.MethodSession, http.MethodGet, "/api/v1/podcasts/41")
	if got.status != http.StatusOK {
		t.Fatalf("status %d: %v", got.status, got.body)
	}
	if got.body["spec"] != nil || got.body["transcript"] != nil {
		t.Errorf("an unreadable column was rendered: %v", got.body)
	}
	if got.body["status"] != "failed" || got.body["error"] != "the speech provider answered 503" {
		t.Errorf("the rest of the podcast was lost: %v", got.body)
	}
}

// A podcast is its owner's and nobody else's, asked of every route. The
// podcast each route is asked about is one the action itself accepts, so the
// owner is answered 200 -- however they signed in: whether a key or an agent
// may act for the account is the sign-in gate's question, asked before any
// route (internal/principal).
//
// Another account asking about the same podcast is told exactly what it
// would be told about one that was never made, and nothing is moved,
// deleted or queued.
func TestAPodcastIsItsOwnersAndNobodyElses(t *testing.T) {
	routes := []struct {
		name, verb, path string
		row              func(*PodcastOfOwnerRow)
	}{
		{"detail", http.MethodGet, "/api/v1/podcasts/%s", nil},
		{"approve", http.MethodPost, "/api/v1/podcasts/%s/brief/approve", nil},
		{"regenerate", http.MethodPost, "/api/v1/podcasts/%s/transcript/regenerate",
			func(row *PodcastOfOwnerRow) { row.Status, row.ArtifactID = statusReady, pointer(episode) }},
		{"revert", http.MethodPost, "/api/v1/podcasts/%s/regenerate/revert",
			func(row *PodcastOfOwnerRow) { row.ArtifactID = pointer(episode) }},
		{"cancel", http.MethodPost, "/api/v1/podcasts/%s/cancel", nil},
		{"delete", http.MethodDelete, "/api/v1/podcasts/%s", nil},
	}
	for _, route := range routes {
		cases := []struct {
			name    string
			id      string
			asker   string
			method  principal.Method
			status  int
			message string
		}{
			{"the owner", "41", callerID, principal.MethodSession, 200, ""},
			{"the owner by an API key", "41", callerID, principal.MethodPersonalAccessToken, 200, ""},
			{"the owner by an agent", "41", callerID, principal.MethodAgent, 200, ""},
			// Not theirs: the same answer as a podcast that is not there.
			{"another account", "41", strangerID, principal.MethodSession, 404, podcastAbsent},
			{"another account by an API key", "41", strangerID, principal.MethodPersonalAccessToken, 404, podcastAbsent},
			{"a podcast that does not exist", "42", callerID, principal.MethodSession, 404, podcastAbsent},
			{"an id of twenty digits", "99999999999999999999", callerID, principal.MethodSession, 404, podcastAbsent},
			{"an id that is a word", "voices-of-the-sea", callerID, principal.MethodSession, 422, ""},
		}
		for _, c := range cases {
			t.Run(route.name+"/"+c.name, func(t *testing.T) {
				before := podcast(41, route.row)
				f := newFake(before)
				f.asker = c.asker
				got := ask(t, f, c.method, route.verb, strings.Replace(route.path, "%s", c.id, 1))
				if got.status != c.status {
					t.Fatalf("status %d, want %d: %v", got.status, c.status, got.body)
				}
				if c.message != "" && got.message() != c.message {
					t.Errorf("told %q, want %q", got.message(), c.message)
				}
				if c.status >= 400 {
					// Nothing is written and nothing queued by a request
					// that was refused: the row is exactly as it was.
					if after, still := f.podcasts[41]; !still || !reflect.DeepEqual(after, before) || f.moves != 0 || len(f.enqueued) != 0 {
						t.Errorf("a refused request changed something: %v, %v", f.podcasts, f.enqueued)
					}
					// The envelope every refusal of this tier has.
					envelope, _ := got.body["error"].(map[string]any)
					if envelope["request_id"] != "req_test" || envelope["status"] != float64(c.status) {
						t.Errorf("the envelope is %v", envelope)
					}
					// And it says nothing of the podcast it refused.
					if strings.Contains(string(got.raw), before.Title) {
						t.Errorf("the refusal carries the owner's title: %s", got.raw)
					}
				}
				if got.header.Get("Access-Control-Allow-Origin") != origin {
					t.Errorf("the answer carries no CORS header: %v", got.header)
				}
			})
		}
	}
}

// The three transitions against every state a podcast can be in.
func TestTheLifecycle(t *testing.T) {
	const (
		approvePath    = "/api/v1/podcasts/41/brief/approve"
		regeneratePath = "/api/v1/podcasts/41/transcript/regenerate"
		revertPath     = "/api/v1/podcasts/41/regenerate/revert"
	)
	cases := []struct {
		name    string
		path    string
		from    string
		brief   string // "" for none
		episode bool
		status  int
		to      string // the status afterwards
		message string
		drafts  bool
	}{
		{"approve a reviewed brief", approvePath, statusAwaitingBrief, goodBrief, false, 200, statusDrafting, "", true},
		{"approve the new brief of a regeneration", approvePath, statusAwaitingBrief, goodBrief, true, 200, statusDrafting, "", true},
		{"approve with no brief", approvePath, statusAwaitingBrief, "", false, 422, statusAwaitingBrief, noBriefToDraft, false},
		{"approve a brief that is json null", approvePath, statusAwaitingBrief, "null", false, 422, statusAwaitingBrief, noBriefToDraft, false},
		{"approve a brief that is an empty object", approvePath, statusAwaitingBrief, "{}", false, 422, statusAwaitingBrief, briefUnreadable, false},
		{"approve a brief that cannot be read", approvePath, statusAwaitingBrief, brokenBrief, false, 422, statusAwaitingBrief, briefUnreadable, false},
		{"approve twice", approvePath, statusDrafting, goodBrief, false, 409, statusDrafting, "drafting -> drafting is not allowed", false},
		{"approve a finished episode", approvePath, statusReady, goodBrief, true, 409, statusReady, "ready -> drafting is not allowed", false},
		{"approve before a brief was proposed", approvePath, statusPending, "", false, 409, statusPending, "pending -> drafting is not allowed", false},
		{"approve a cancelled podcast", approvePath, statusCancelled, goodBrief, false, 409, statusCancelled, "cancelled -> drafting is not allowed", false},
		{"approve a failed podcast", approvePath, statusFailed, goodBrief, false, 409, statusFailed, "failed -> drafting is not allowed", false},

		{"regenerate a finished episode", regeneratePath, statusReady, goodBrief, true, 200, statusAwaitingBrief, "", false},
		{"regenerate from the old review gate", regeneratePath, statusAwaitingReview, goodBrief, false, 200, statusAwaitingBrief, "", false},
		{"regenerate an episode from before briefs", regeneratePath, statusReady, "", true, 422, statusReady, noBriefToRedo, false},
		{"regenerate an episode whose brief cannot be read", regeneratePath, statusReady, brokenBrief, true, 422, statusReady, briefUnreadable, false},
		{"regenerate a podcast still pending", regeneratePath, statusPending, goodBrief, false, 409, statusPending, nothingToRedo + "pending", false},
		{"regenerate while drafting", regeneratePath, statusDrafting, goodBrief, true, 409, statusDrafting, nothingToRedo + "drafting", false},
		{"regenerate at the brief gate", regeneratePath, statusAwaitingBrief, goodBrief, true, 409, statusAwaitingBrief, nothingToRedo + "awaiting_brief", false},
		{"regenerate a failed podcast", regeneratePath, statusFailed, goodBrief, false, 409, statusFailed, nothingToRedo + "failed", false},

		{"revert at the brief gate", revertPath, statusAwaitingBrief, goodBrief, true, 200, statusReady, "", false},
		{"revert while drafting", revertPath, statusDrafting, goodBrief, true, 200, statusReady, "", false},
		{"revert while rendering", revertPath, statusRendering, goodBrief, true, 200, statusReady, "", false},
		// Reverting does not read the brief, so a broken one is no obstacle.
		{"revert with a brief that cannot be read", revertPath, statusDrafting, brokenBrief, true, 200, statusReady, "", false},
		{"revert a first take", revertPath, statusDrafting, goodBrief, false, 409, statusDrafting, noEpisode, false},
		{"revert what is not being regenerated", revertPath, statusReady, goodBrief, true, 409, statusReady, "ready -> ready is not allowed", false},
		{"revert a failed podcast", revertPath, statusFailed, goodBrief, true, 409, statusFailed, "failed -> ready is not allowed", false},
		{"revert a cancelled podcast", revertPath, statusCancelled, goodBrief, true, 409, statusCancelled, "cancelled -> ready is not allowed", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFake(podcast(41, func(row *PodcastOfOwnerRow) {
				row.Status = c.from
				row.Spec = nil
				if c.brief != "" {
					row.Spec = text(c.brief)
				}
				if c.episode {
					row.ArtifactID = pointer(episode)
				}
			}))
			got := ask(t, f, principal.MethodSession, http.MethodPost, c.path)
			if got.status != c.status {
				t.Fatalf("status %d, want %d: %v", got.status, c.status, got.body)
			}
			if c.message != "" && got.message() != c.message {
				t.Errorf("told %q, want %q", got.message(), c.message)
			}
			if left := f.podcasts[41].Status; left != c.to {
				t.Errorf("the podcast is left %q, want %q", left, c.to)
			}
			if c.status == http.StatusOK {
				if got.body["status"] != c.to {
					t.Errorf("the answer says %v and the row says %q", got.body["status"], c.to)
				}
				// The episode's id is answered by the transitions too.
				if c.episode && (got.body["artifact_id"] != float64(episode) || got.body["has_audio"] != true) {
					t.Errorf("the answer lost the episode: %v", got.body)
				}
			}

			var want []produce.Message
			if c.drafts {
				// The task's two arguments, and the request's id for its logs.
				// The queue is the routing table's: nothing here names one.
				message := produce.Delay(DraftTask, int64(41))
				message.Context = produce.LogContext{RequestID: "req_test", UserID: callerID}
				want = append(want, message)
			}
			if !reflect.DeepEqual(f.enqueued, want) {
				t.Errorf("enqueued %+v, want %+v", f.enqueued, want)
			}
		})
	}
}

// Two approvals at once: the second finds the podcast already moved, and
// must not start a second draft.
func TestAnApprovalThatLosesARaceIsAConflictAndDraftsNothing(t *testing.T) {
	f := newFake(podcast(41, nil))
	f.meanwhile = func(other *fake) {
		row := other.podcasts[41]
		row.Status = statusDrafting
		other.podcasts[41] = row
	}
	got := ask(t, f, principal.MethodSession, http.MethodPost, "/api/v1/podcasts/41/brief/approve")
	if got.status != http.StatusConflict || got.message() != changedMeanwhile {
		t.Fatalf("status %d, told %q", got.status, got.message())
	}
	if len(f.enqueued) != 0 {
		t.Errorf("a second draft was queued: %+v", f.enqueued)
	}
}

func TestATransitionOfAPodcastDeletedMeanwhileIsAConflict(t *testing.T) {
	f := newFake(podcast(41, func(row *PodcastOfOwnerRow) { row.ArtifactID = pointer(episode) }))
	f.meanwhile = func(other *fake) { delete(other.podcasts, 41) }
	got := ask(t, f, principal.MethodSession, http.MethodPost, "/api/v1/podcasts/41/regenerate/revert")
	if got.status != http.StatusConflict {
		t.Fatalf("status %d: %v", got.status, got.body)
	}
}

// A broker that is down must not leave the podcast `drafting` with nothing
// drafting it.
func TestAnApprovalWhoseDraftCannotBeQueuedIsUndone(t *testing.T) {
	f := newFake(podcast(41, nil))
	f.queueFails = true
	got := ask(t, f, principal.MethodSession, http.MethodPost, "/api/v1/podcasts/41/brief/approve")
	if got.status != http.StatusServiceUnavailable || got.message() != queueFailed {
		t.Fatalf("status %d, told %q", got.status, got.message())
	}
	if left := f.podcasts[41].Status; left != statusAwaitingBrief {
		t.Errorf("the podcast is left %q; the approval should have been undone", left)
	}
	// And it can simply be approved again once the broker is back.
	f.queueFails = false
	again := ask(t, f, principal.MethodSession, http.MethodPost, "/api/v1/podcasts/41/brief/approve")
	if again.status != http.StatusOK || len(f.enqueued) != 1 {
		t.Errorf("the second approval: status %d, queued %d", again.status, len(f.enqueued))
	}
}

func TestADeleteRemovesTheRowInAnyState(t *testing.T) {
	for _, status := range []string{
		statusPending, statusAwaitingBrief, statusDrafting, statusAwaitingReview,
		statusRendering, statusReady, statusFailed, statusCancelled,
	} {
		t.Run(status, func(t *testing.T) {
			f := newFake(
				// A brief nobody can read is no obstacle to deleting it.
				podcast(41, func(row *PodcastOfOwnerRow) { row.Status, row.Spec = status, text(brokenBrief) }),
				podcast(43, nil),
			)
			got := ask(t, f, principal.MethodSession, http.MethodDelete, "/api/v1/podcasts/41")
			if got.status != http.StatusOK || got.body["message"] != deleted {
				t.Fatalf("status %d: %v", got.status, got.body)
			}
			if _, still := f.podcasts[41]; still {
				t.Error("the podcast is still there")
			}
			if _, still := f.podcasts[43]; !still {
				t.Error("another podcast was deleted with it")
			}
		})
	}
}

// Deleted by somebody else between the read and the delete is what the
// caller wanted.
func TestADeleteOfAPodcastDeletedMeanwhileIsStillDone(t *testing.T) {
	f := newFake(podcast(41, nil))
	f.meanwhile = func(other *fake) { delete(other.podcasts, 41) }
	got := ask(t, f, principal.MethodSession, http.MethodDelete, "/api/v1/podcasts/41")
	if got.status != http.StatusOK {
		t.Fatalf("status %d: %v", got.status, got.body)
	}
}
