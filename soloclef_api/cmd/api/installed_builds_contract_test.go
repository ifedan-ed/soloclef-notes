package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/config"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/health"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/installedbuilds"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/signin"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	filestore "git.danvics.com/danvics/soloclef/soloclef_filestore"
	"github.com/valkey-io/valkey-go"
)

// The installed phone builds' contract, run against the real server.
//
// Every request an installed build makes (internal/installedbuilds,
// calls.json) is sent, with the query strings and bodies the build sends,
// through serve() -- the whole server: every route, the credential gate,
// the CSRF check, the rate limiter and the path guard -- backed by a real
// Postgres holding the migrated schema and a real Valkey. Each answer must
// be the status the case names, never a 5xx unless the case names the
// service this test does not run, and, where the build expects JSON, an
// answer every build that makes the call can read (installedbuilds'
// CheckAnswer).
//
// Why the real server and the real database: on 2026-11-13 the shape checks
// of the announcements fix passed, and the Inbox still answered 500, because
// the placeholder type it queried with held a NUL that Postgres refuses.
// Only a statement that reaches Postgres finds that.
//
// The services this test does not run (the model proxy, Qdrant, the parse
// service, Google, Microsoft, Nextcloud, the sandbox) are given addresses
// nothing listens on, so a route that needs one fails fast, and its case
// says so (contractCase.Unavailable).
const (
	// contractDatabase names a throwaway Postgres. **Every account in it is
	// deleted** (internal/testpostgres).
	contractDatabase = "SOLOCLEF_API_TEST_CONTRACT_DATABASE"
	// contractValkey names a throwaway Valkey (redis://host:port/0): the
	// limiter counts in it and the queue publishes to it.
	contractValkey = "SOLOCLEF_API_TEST_CONTRACT_VALKEY"
	// contractRequired, "true", makes a missing database or Valkey a
	// failure rather than a skip. CI sets it (.forgejo/workflows/go.yml), so
	// the contract is never skipped there by a variable that went missing.
	contractRequired = "SOLOCLEF_API_TEST_CONTRACT_REQUIRED"
)

// A contractCase is one request an installed build makes.
type contractCase struct {
	// Operation is the call as calls.json names it: "GET /api/v1/notes".
	Operation string
	// Path is what the build sends: the path with the seed's ids and the
	// query string. {{name}} is replaced by a value an earlier case
	// captured.
	Path string
	// Body is the JSON body the build sends, or "" for none.
	Body string
	// Upload is a multipart body's fields, sent as the app's upload task
	// sends them (fields first, then each file); nil for none.
	Upload *upload
	// Status is the answer the build must get.
	Status int
	// Why is required when Status is not a success: what the build does
	// with it, and why that answer is right.
	Why string
	// Anonymous sends no credential (a public page, the sign-in).
	Anonymous bool
	// Unavailable names the service this test does not run that the route
	// needs. Only then may Status be a 5xx.
	Unavailable string
	// Capture names values of the answer for later cases: {"refresh":
	// "refresh_token"} takes the top-level field refresh_token.
	Capture map[string]string
	// Bearer is the credential to send instead of Alice's session:
	// "{{name}}" of a captured token.
	Bearer string
	// Bundles are the snapshots (their directories) whose request this is,
	// when bundles send the same call differently (a fixed body in a later
	// update); empty is every bundle that makes the call.
	Bundles []string
}

// upload is a multipart form as the app's upload task builds it.
type upload struct {
	Fields map[string]string
	Files  map[string]string // form field → file name; the content is a short text
}

// A contractArea is a part of the product with the rows its cases need.
// Each area's seed uses ids of its own range so that one area's writes
// never touch another's rows.
type contractArea struct {
	Name string
	// Order is where the area runs: 10-89 for the product's areas, 90 and
	// above for what ends the session (the sign-out, the account's delete).
	Order int
	Seed  string
	// Files is what the seed's rows point at in the file store: storage
	// key → content, written through the server's own local store before
	// it starts (a study write reads its deck, a download streams its
	// file).
	Files map[string]string
	Cases []contractCase
}

// contractAreas is every area of the installed builds' contract, in the
// order it runs (contractArea.Order): the reads of an area before its
// writes, and the sign-out last of all, because it ends the session every
// other case uses. Each area is a file of its own
// (installed_builds_area_*_test.go) that registers itself, so areas are
// added without touching a shared list.
func contractAreas() []contractArea {
	areas := append([]contractArea{}, registeredAreas...)
	sort.SliceStable(areas, func(i, j int) bool { return areas[i].Order < areas[j].Order })
	return areas
}

var registeredAreas []contractArea

func registerArea(area contractArea) { registeredAreas = append(registeredAreas, area) }

// contractSessionSecret is the contract deployment's session secret, which
// is also what every stored credential is sealed under: a seed that stores
// a connection's credential seals it with this, as the server would.
const contractSessionSecret = "contract-test-session-secret-0123456789abcdef"

// contractPassword is Alice's password in the throwaway database.
const contractPassword = "contract-password-for-a-throwaway-database"

func TestInstalledBuildsAgainstTheRealServer(t *testing.T) {
	for _, variable := range []string{contractDatabase, contractValkey} {
		if os.Getenv(variable) == "" {
			if os.Getenv(contractRequired) == "true" {
				t.Fatalf("%s is not set and %s is true: the installed builds' contract must run here", variable, contractRequired)
			}
			t.Skipf("%s is not set; this test needs a throwaway Postgres and Valkey (internal/installedbuilds/README.md)", variable)
		}
	}
	builds := loadInstalledBuilds(t)
	pool := testpostgres.Open(t, contractDatabase)
	ctx := context.Background()
	for _, area := range contractAreas() {
		if area.Seed != "" {
			testpostgres.Exec(t, pool, area.Seed)
		}
	}
	if _, _, err := signin.SetPassword(ctx, pool, signin.NewHasher(1), "alice@example.test", contractPassword, time.Now().UTC()); err != nil {
		t.Fatalf("setting Alice's password: %v", err)
	}
	settings := contractSettings(t)
	writeContractFiles(t, settings)
	emptyContractValkey(t)
	address := startContractServer(t, settings)
	session, err := signin.MintSession(ctx, pool, settings.SessionSecret, testpostgres.Alice, time.Now().UTC())
	if err != nil {
		t.Fatalf("minting Alice's session: %v", err)
	}

	// Which of the snapshots' exceptions an answer needed: one never
	// needed is stale and fails below.
	used := map[string]bool{}
	captured := map[string]string{}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, area := range contractAreas() {
		for _, c := range area.Cases {
			path := fill(t, c.Path, captured)
			status, body, err := sendCase(client, address, path, c, session, captured)
			if err != nil {
				t.Errorf("%s %s (%s): %v", method(c), path, area.Name, err)
				continue
			}
			for _, problem := range judge(builds, c, status, body, used) {
				t.Errorf("%s %s (%s): %s", method(c), path, area.Name, problem)
			}
			for name, field := range c.Capture {
				if value, ok := captureField(body, field); ok {
					captured[name] = value
				} else {
					t.Errorf("%s %s: no %q in the answer to capture as %s: %.300s", method(c), path, field, name, body)
				}
			}
		}
	}
	for _, build := range builds {
		for index := range build.Exceptions {
			if id := installedbuilds.ExceptionID(build, index); !used[id] {
				t.Errorf("%s was not needed by any answer: the server or the bundle no longer differs there; delete it", id)
			}
		}
	}
}

// captureField reads a value out of a JSON answer by a dotted path, where a
// number is a list's index: "document_ids.0", "items.1.id".
func captureField(body []byte, field string) (string, bool) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return "", false
	}
	for _, step := range strings.Split(field, ".") {
		switch node := value.(type) {
		case map[string]any:
			value = node[step]
		case []any:
			index, err := strconv.Atoi(step)
			if err != nil || index < 0 || index >= len(node) {
				return "", false
			}
			value = node[index]
		default:
			return "", false
		}
	}
	if value == nil {
		return "", false
	}
	return fmt.Sprint(value), true
}

// writeContractFiles puts each area's files where the server's local store
// will read them, through the store's own writer.
func writeContractFiles(t *testing.T, settings config.Config) {
	t.Helper()
	local, err := filestore.OpenLocal(settings.FileStorageLocalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	for _, area := range contractAreas() {
		for key, content := range area.Files {
			if err := local.Put(context.Background(), key, []byte(content), "application/octet-stream"); err != nil {
				t.Fatalf("%s: writing %s: %v", area.Name, key, err)
			}
		}
	}
}

// emptyContractValkey starts the run with no counters: the sign-in cases
// spend password attempts, and a second run within the limiter's minute
// would otherwise be refused 429 for the first run's.
func emptyContractValkey(t *testing.T) {
	t.Helper()
	options, err := valkey.ParseURL(os.Getenv(contractValkey))
	if err != nil {
		t.Fatalf("%s: %v", contractValkey, err)
	}
	client, err := valkey.NewClient(options)
	if err != nil {
		t.Fatalf("connecting to %s: %v", contractValkey, err)
	}
	defer client.Close()
	if err := client.Do(context.Background(), client.B().Flushdb().Build()).Error(); err != nil {
		t.Fatalf("emptying the throwaway Valkey: %v", err)
	}
}

// judge says how an answer breaks the bundles that send the request, and
// marks in used each snapshot exception that excused its status.
func judge(builds []installedbuilds.Build, c contractCase, status int, body []byte, used map[string]bool) []string {
	var problems []string
	if status >= 500 && c.Unavailable == "" {
		problems = append(problems, fmt.Sprintf("answered %d: a server error on a request installed builds make. %.400s", status, body))
		return problems
	}
	represented := sending(builds, c)
	if status != c.Status {
		// Excused only when every bundle that sends this request documents
		// this answer as its own fault, fixed by an update.
		excused := len(represented) > 0
		for _, sender := range represented {
			excused = excused && sender.build.ExcusedStatus(c.Operation, status) >= 0
		}
		if !excused {
			return append(problems, fmt.Sprintf("answered %d, want %d. %.400s", status, c.Status, body))
		}
		for _, sender := range represented {
			used[installedbuilds.ExceptionID(sender.build, sender.build.ExcusedStatus(c.Operation, status))] = true
		}
		return problems
	}
	if status < 200 || status > 299 {
		return problems
	}
	// One line per way the answer breaks, naming every bundle it breaks:
	// the bundles often share a snapshot, and three copies of one problem
	// hide the next one.
	var order []string
	breaks := map[string][]string{}
	for _, sender := range represented {
		if !sender.call.InSnapshot {
			continue
		}
		found := sender.build.CheckAnswer(sender.call, status, body)
		for _, problem := range found {
			if _, seen := breaks[problem]; !seen {
				order = append(order, problem)
			}
			breaks[problem] = append(breaks[problem], sender.build.Label())
		}
	}
	for _, problem := range order {
		problems = append(problems, fmt.Sprintf("%s -- breaks %s", problem, strings.Join(breaks[problem], "; ")))
	}
	return problems
}

// sender is a bundle that sends a case's request, with its call.
type sender struct {
	build installedbuilds.Build
	call  installedbuilds.Call
}

// sending is the bundles a case speaks for: those that make the call,
// narrowed to the case's Bundles when it names them.
func sending(builds []installedbuilds.Build, c contractCase) []sender {
	var out []sender
	for _, build := range builds {
		if len(c.Bundles) > 0 && !slices.Contains(c.Bundles, build.Dir) {
			continue
		}
		for _, call := range build.Calls {
			if installedbuilds.Shape(call.Operation()) == installedbuilds.Shape(c.Operation) {
				out = append(out, sender{build, call})
				break
			}
		}
	}
	return out
}

func method(c contractCase) string { return strings.SplitN(c.Operation, " ", 2)[0] }

var placeholder = regexp.MustCompile(`\{\{(\w+)\}\}`)

func fill(t *testing.T, text string, captured map[string]string) string {
	t.Helper()
	return placeholder.ReplaceAllStringFunc(text, func(found string) string {
		name := placeholder.FindStringSubmatch(found)[1]
		value, ok := captured[name]
		if !ok {
			t.Errorf("%s: nothing captured as %s yet", text, name)
		}
		return value
	})
}

func sendCase(client *http.Client, address, path string, c contractCase, session string, captured map[string]string) (int, []byte, error) {
	var body io.Reader
	contentType := ""
	switch {
	case c.Upload != nil:
		var form bytes.Buffer
		writer := multipart.NewWriter(&form)
		for name, value := range c.Upload.Fields {
			_ = writer.WriteField(name, placeholder.ReplaceAllStringFunc(value, func(found string) string {
				return captured[placeholder.FindStringSubmatch(found)[1]]
			}))
		}
		for field, name := range c.Upload.Files {
			part, _ := writer.CreateFormFile(field, name)
			_, _ = part.Write([]byte("A short text a phone uploaded for the contract test.\n"))
		}
		_ = writer.Close()
		body, contentType = &form, writer.FormDataContentType()
	case c.Body != "":
		body, contentType = strings.NewReader(placeholder.ReplaceAllStringFunc(c.Body, func(found string) string {
			return captured[placeholder.FindStringSubmatch(found)[1]]
		})), "application/json"
	}
	request, err := http.NewRequest(method(c), "http://"+address+path, body)
	if err != nil {
		return 0, nil, err
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	// What the app sends on every request (lib/api/client.ts).
	request.Header.Set("X-SurfSense-Client-Platform", "mobile")
	switch {
	case c.Bearer != "":
		request.Header.Set("Authorization", "Bearer "+placeholder.ReplaceAllStringFunc(c.Bearer, func(found string) string {
			return captured[placeholder.FindStringSubmatch(found)[1]]
		}))
	case !c.Anonymous:
		request.Header.Set("Authorization", "Bearer "+session)
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	// A stream is read for a while and no longer: the status and its first
	// bytes are the contract; a chat turn's stream may run on.
	answer, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return response.StatusCode, answer, fmt.Errorf("reading the answer: %w", err)
	}
	return response.StatusCode, answer, nil
}

// contractSettings is a deployment of the server that points at the
// throwaway Postgres and Valkey, and at nothing for every other service.
func contractSettings(t *testing.T) config.Config {
	t.Helper()
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "litellm-key")
	if err := os.WriteFile(keyFile, []byte("contract-test-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Nothing listens on port 9 (discard) on a test machine: a route that
	// calls one of these services is refused at once.
	const nowhere = "http://127.0.0.1:9"
	valkey := os.Getenv(contractValkey)
	values := map[string]string{
		"LISTEN_ADDRESS":                 "127.0.0.1:0",
		"LOG_LEVEL":                      "error",
		"SHUTDOWN_GRACE_SECONDS":         "5",
		"DATABASE_URL":                   os.Getenv(contractDatabase),
		"DATABASE_MAX_CONNECTIONS":       "8",
		"SESSION_SECRET":                 contractSessionSecret,
		"REDIS_URL":                      valkey,
		"CELERY_BROKER_URL":              valkey,
		"CELERY_DEFAULT_QUEUE":           "contract",
		"ERROR_REPORT_URL":               "https://errors.example.test/report",
		"PUBLIC_URL":                     "https://notes.example.test",
		"FRONTEND_URL":                   "https://notes.example.test",
		"ALLOWED_ORIGINS":                "https://notes.example.test",
		"FILE_STORAGE_BACKEND":           "local",
		"FILE_STORAGE_LOCAL_PATH":        filepath.Join(dir, "files"),
		"LITELLM_URL":                    nowhere,
		"LITELLM_KEY_FILE":               keyFile,
		"CHAT_MODEL":                     "contract-chat",
		"EMBEDDING_MODEL":                "contract-embedding",
		"EMBEDDING_DIMENSION":            "8",
		"QDRANT_URL":                     nowhere,
		"QDRANT_CHUNK_COLLECTION":        "contract",
		"QDRANT_SEARCH_HNSW_EF":          "64",
		"QDRANT_QUANTIZED":               "false",
		"QDRANT_SEARCH_RESCORE":          "false",
		"QDRANT_SEARCH_OVERSAMPLING":     "1",
		"NCBI_TOOL":                      "soloclef-contract-test",
		"CHAT_MAX_STEPS":                 "4",
		"CHAT_MAX_TURN_SECONDS":          "30",
		"SANDBOX_ENABLED":                "false",
		"SIGNUP_ALLOWED":                 "false",
		"OIDC_ENABLED":                   "false",
		"SMTP_ENABLED":                   "false",
		"E2E_ROUTES":                     "false",
		"UPLOAD_MAX_FILES":               "10",
		"UPLOAD_MAX_FILE_BYTES":          "10485760",
		"NOTE_INDEX_DELAY_SECONDS":       "5",
		"NEXTCLOUD_BASE_URL":             nowhere,
		"NEXTCLOUD_DOWNLOAD_CONCURRENCY": "1",
		"SUPPORT_EMAIL":                  "support@example.test",
		// No poster is drawn by a contract case: a resvg that only says
		// its version and a font directory with one empty face let the
		// server start as a deployment does, which checks both.
		"FIGURES_RESVG":    filepath.Join(dir, "resvg"),
		"FIGURES_FONT_DIR": filepath.Join(dir, "fonts"),
	}
	if err := os.MkdirAll(values["FILE_STORAGE_LOCAL_PATH"], 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(values["FIGURES_RESVG"], []byte("#!/bin/sh\necho 0.48.1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(values["FIGURES_FONT_DIR"], 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(values["FIGURES_FONT_DIR"], "Stand-in.ttf"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err := config.Load(func(name string) string { return values[strings.TrimPrefix(name, "SOLOCLEF_API_")] })
	if err != nil {
		t.Fatalf("the contract deployment's settings: %v", err)
	}
	return settings
}

// contractParsers is the parsers' own settings (soloclef_ingest/parserconfig):
// the local parse service, at an address nothing listens on.
func contractParsers(name string) string {
	return map[string]string{
		"SOLOCLEF_PARSER_DEFAULT":                "local",
		"SOLOCLEF_PARSE_MAX_TEXT_BYTES":          "1048576",
		"SOLOCLEF_PARSE_MAX_MARKDOWN_BYTES":      "1048576",
		"SOLOCLEF_PARSE_SERVICE_URL":             "http://127.0.0.1:9",
		"SOLOCLEF_PARSE_SERVICE_TIMEOUT_SECONDS": "5",
	}[name]
}

// startContractServer runs serve() until the test ends and answers its
// address once the probe says it is serving.
func startContractServer(t *testing.T, settings config.Config) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	var served error
	go func() {
		defer close(stopped)
		served = serve(ctx, settings, contractParsers, listener)
	}()
	t.Cleanup(func() {
		stop()
		<-stopped
		if served != nil {
			t.Errorf("the server stopped with %v", served)
		}
	})
	address := listener.Addr().String()
	deadline := time.Now().Add(30 * time.Second)
	for {
		select {
		case <-stopped:
			t.Fatalf("the server did not start: %v", served)
		default:
		}
		if response, err := http.Get("http://" + address + health.Path); err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return address
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the server did not answer its probe within 30 s")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
