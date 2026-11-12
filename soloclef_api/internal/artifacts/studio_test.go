package artifacts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	"git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// studio is everything the ten file-reading routes stand on, for one test:
// the database as a stand-in, and the file store as the real thing -- a
// filestore.Local over a temporary directory, so that what these tests
// stream really is read off a disk. Every file in it is written by the test.
type studio struct {
	// asker is the account the requests are made as: callerID, who owns
	// every row below, unless a test says otherwise.
	asker string

	// The rows, all callerID's. A nil row is an artifact the owner does not
	// have; to any other account every row is nil (yours).
	manifest *ManifestRow
	download *Download
	file     *StoredFile
	slides   *SlideSet
	source   *StudySource

	// sections is the artifact's metadata as far as study state goes:
	// section name to the object mapping user id to state.
	sections map[string]string
	// meanwhile runs once, between a study write's read and its lock:
	// somebody else getting to the artifact first.
	meanwhile func(s *studio)
	// writes counts the study writes that reached the row.
	writes int
	// databaseDown fails every statement.
	databaseDown bool

	// files is the local store `put` writes into, and stores the set the
	// routes read: that store alone, unless a test gives it another.
	files     filestore.Store
	stores    *filestore.Stores
	directory string
	log       *bytes.Buffer
}

func newStudio(t testing.TB) *studio {
	t.Helper()
	directory := t.TempDir()
	store, err := filestore.OpenLocal(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return &studio{
		asker:     callerID,
		sections:  map[string]string{},
		files:     store,
		stores:    setOf(t, store),
		directory: directory,
		log:       &bytes.Buffer{},
	}
}

// setOf is a set of stores for the routes to read: `configured` is the
// backend the deployment writes to, and the others are read beside it.
func setOf(t testing.TB, configured filestore.Store, alsoRead ...filestore.Store) *filestore.Stores {
	t.Helper()
	stores, err := filestore.NewStores(configured, alsoRead...)
	if err != nil {
		t.Fatal(err)
	}
	return stores
}

// put stores an object under a key.
func (s *studio) put(t testing.TB, key string, content []byte) {
	t.Helper()
	path := filepath.Join(s.directory, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

var errDatabaseDown = errors.New("the database is unreachable")

// yours is the owner predicate every statement carries, as this stand-in
// keeps it: the rows are callerID's, and a statement run for anyone else
// finds none. A route that passed anything but the signed-in caller's id
// would be caught here.
func yours[T any](userID string, row *T) *T {
	if userID != callerID {
		return nil
	}
	return row
}

func (s *studio) ManifestOf(_ context.Context, userID string, _ int64) (*ManifestRow, error) {
	if s.databaseDown {
		return nil, errDatabaseDown
	}
	return yours(userID, s.manifest), nil
}

func (s *studio) DownloadOf(_ context.Context, userID string, _ int64) (*Download, error) {
	if s.databaseDown {
		return nil, errDatabaseDown
	}
	return yours(userID, s.download), nil
}

func (s *studio) FileIn(_ context.Context, userID string, _, _ int64) (*StoredFile, error) {
	if s.databaseDown {
		return nil, errDatabaseDown
	}
	return yours(userID, s.file), nil
}

func (s *studio) SlidesOf(_ context.Context, userID string, _ int64) (*SlideSet, error) {
	if s.databaseDown {
		return nil, errDatabaseDown
	}
	return yours(userID, s.slides), nil
}

func (s *studio) StudySourceOf(_ context.Context, userID string, _ int64) (*StudySource, error) {
	if s.databaseDown {
		return nil, errDatabaseDown
	}
	if yours(userID, s.source) == nil {
		return nil, nil
	}
	copied := *s.source
	return &copied, nil
}

// ChangeStudy is the transaction as the route sees it: the row as it is
// when the lock is taken, the decision, and the write.
func (s *studio) ChangeStudy(_ context.Context, userID string, _ int64, section, _ string,
	decide func(*LockedStudy) StudyWrite) error {
	if s.meanwhile != nil {
		other := s.meanwhile
		s.meanwhile = nil
		other(s)
	}
	if s.databaseDown {
		return errDatabaseDown
	}
	if yours(userID, s.source) == nil {
		decide(nil)
		return nil
	}
	locked := &LockedStudy{Format: s.source.Format, Generation: s.source.Generation}
	if states, held := s.sections[section]; held {
		locked.States = []byte(states)
	}
	write := decide(locked)
	if write.Abandon {
		return nil
	}
	s.writes++
	if write.States == nil {
		delete(s.sections, section)
	} else {
		s.sections[section] = string(write.States)
	}
	return nil
}

// call sends one request to the ten routes, mounted as cmd/api mounts them:
// behind the request-id and CORS middlewares, and behind the body reader
// that buffers a write's body before the route runs.
func (s *studio) call(t *testing.T, method principal.Method, verb, path string, header http.Header, body string) answered {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(s.log, nil))
	gate := Gate{Errors: errorenvelope.Writer{ReportURL: origin + "/contact"}, Logger: logger}
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "0.0.1"))
	// A reader of study files per request, as a fresh process would have:
	// tests rewrite a key's file between calls, which production never does.
	studyFiles := NewStudyFiles(s.stores)
	ManifestRoute{Gate: gate, Manifests: s, StudyFiles: studyFiles}.Register(api)
	DownloadRoute{Gate: gate, Downloads: s, Files: s.stores, StallTimeout: time.Minute}.Register(api)
	FileContentRoute{Gate: gate, Rows: s, Files: s.stores, StallTimeout: time.Minute}.Register(api)
	SlideAudioRoute{Gate: gate, Slides: s, Files: s.stores, StallTimeout: time.Minute}.Register(api)
	FlashcardRoutes{Gate: gate, Study: s, StudyFiles: studyFiles}.Register(api)
	QuizRoutes{Gate: gate, Study: s, StudyFiles: studyFiles}.Register(api)

	withCaller := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mux.ServeHTTP(response, request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: s.asker, Method: method})))
	})
	withBodies := requestbody.Middleware{
		Covers: requestbody.CoveringRoutes(
			http.MethodPatch+" "+FlashcardProgressPath,
			http.MethodPut+" "+FlashcardOrderPath,
			http.MethodPut+" "+QuizAnswerPath,
			http.MethodPut+" "+QuizSkipPath,
			http.MethodPost+" "+QuizRetakePath,
		),
	}.Wrap(withCaller)
	server := httptest.NewServer(requestcontext.Middleware(logger,
		corsmirror.NewPolicy([]string{origin}).Middleware(withBodies)))
	defer server.Close()

	var payload io.Reader
	if body != "" {
		payload = strings.NewReader(body)
	}
	request, err := http.NewRequest(verb, server.URL+path, payload)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Request-ID", "req_test")
	request.Header.Set("Origin", origin)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, values := range header {
		request.Header[name] = values
	}
	// The transport would otherwise ask for gzip and hide Content-Length.
	request.Header.Set("Accept-Encoding", "identity")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("the body did not arrive whole: %v", err)
	}
	return answered{status: response.StatusCode, header: response.Header, raw: raw}
}

func (s *studio) get(t *testing.T, path string, header http.Header) answered {
	t.Helper()
	return s.call(t, principal.MethodSession, http.MethodGet, path, header, "")
}

// fields is a 422's failures, as "location: message" lines.
func (a answered) fields(t *testing.T) []string {
	t.Helper()
	var envelope struct {
		Error struct {
			Fields []struct {
				Loc []string `json:"loc"`
				Msg string   `json:"msg"`
			} `json:"fields"`
		} `json:"error"`
	}
	if err := json.Unmarshal(a.raw, &envelope); err != nil {
		t.Fatalf("not an envelope: %s", a.raw)
	}
	lines := make([]string, 0, len(envelope.Error.Fields))
	for _, field := range envelope.Error.Fields {
		lines = append(lines, strings.Join(field.Loc, ".")+": "+field.Msg)
	}
	return lines
}

// The ten routes, for artifact 31.
const (
	base             = "/api/v1/artifacts/31"
	manifestURL      = base + "/manifest"
	downloadURL      = base + "/download"
	fileURL          = base + "/files/88/content"
	slideURL         = base + "/slides/2/audio"
	progressURL      = base + "/flashcard-progress"
	orderURL         = base + "/flashcard-order"
	quizAnswerURL    = base + "/quiz-answer"
	quizSkipURL      = base + "/quiz-skip"
	quizRetakeURL    = base + "/quiz-retake"
	resetURL         = progressURL + "?generation=3"
	deckStorageKey   = "artifacts/7/31/primary/0123456789abcdef0123456789abcdef.json"
	videoStorageKey  = "artifacts/7/31/primary/fedcba9876543210fedcba9876543210.mp4"
	audioStorageKey  = "video_presentations/7/31/slide-2-00112233445566778899aabbccddeeff.mp3"
	inventedChecksum = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
)

// bytesOf is n bytes in which a range that is off by one reads differently.
func bytesOf(n int) []byte {
	content := make([]byte, n)
	for index := range content {
		content[index] = byte(index*7 + index/251)
	}
	return content
}
