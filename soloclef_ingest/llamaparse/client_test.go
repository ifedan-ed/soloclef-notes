package llamaparse

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

// fixture is a reply written by hand from the v2 Parse API's documented
// shapes: id and status at the top level of the create reply, nested under
// "job" on a GET, and markdown.pages with a success flag per page.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	content, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

type reply struct {
	status int
	body   []byte
}

// llamaStub scripts the three kinds of request: the upload, a status check
// (GET without expand) and the result (GET with expand=markdown). The last
// entry of each script repeats.
type llamaStub struct {
	t                          *testing.T
	uploads, statuses, results []reply

	uploadCount, statusCount, resultCount atomic.Int32
	configuration, file, filename, path   string
}

func (s *llamaStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
		s.t.Errorf("Authorization = %q", got)
	}
	var script []reply
	var index int32
	switch {
	case r.Method == http.MethodPost:
		if r.URL.Path != "/api/v2/parse/upload" {
			s.t.Errorf("upload path = %q", r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			s.t.Errorf("the upload is not multipart: %v", err)
		}
		s.configuration = r.MultipartForm.Value["configuration"][0]
		header := r.MultipartForm.File["file"][0]
		file, _ := header.Open()
		content, _ := io.ReadAll(file)
		s.file, s.filename = string(content), header.Filename
		script, index = s.uploads, s.uploadCount.Add(1)-1
	case r.URL.Query().Get("expand") == "markdown":
		s.path = r.URL.Path
		script, index = s.results, s.resultCount.Add(1)-1
	default:
		if r.URL.RawQuery != "" {
			s.t.Errorf("a status check carried a query: %q", r.URL.RawQuery)
		}
		script, index = s.statuses, s.statusCount.Add(1)-1
	}
	answer := script[min(int(index), len(script)-1)]
	w.WriteHeader(answer.status)
	w.Write(answer.body)
}

func start(t *testing.T, stub *llamaStub, timeout time.Duration) *Client {
	t.Helper()
	stub.t = t
	server := httptest.NewServer(stub)
	t.Cleanup(server.Close)
	client, err := New(Config{
		APIKey: "test-key", BaseURL: server.URL, Tier: "agentic", Version: "latest",
		PollInterval: time.Millisecond, Timeout: timeout, MaxResponseBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestParseUploadsWaitsAndReadsThePages(t *testing.T) {
	stub := &llamaStub{
		uploads:  []reply{{200, fixture(t, "created.json")}},
		statuses: []reply{{200, fixture(t, "running.json")}, {200, fixture(t, "completed.json")}},
		results:  []reply{{200, fixture(t, "completed_markdown.json")}},
	}
	client := start(t, stub, 5*time.Second)

	document, err := client.Parse(context.Background(), parse.StringSource("guide.pdf", "%PDF-bytes"))
	if err != nil {
		t.Fatal(err)
	}

	var configuration map[string]any
	if err := json.Unmarshal([]byte(stub.configuration), &configuration); err != nil {
		t.Fatalf("configuration is not JSON: %q", stub.configuration)
	}
	if len(configuration) != 2 || configuration["tier"] != "agentic" || configuration["version"] != "latest" {
		t.Errorf("configuration = %v", configuration)
	}
	if stub.file != "%PDF-bytes" || stub.filename != "guide.pdf" {
		t.Errorf("file = %q named %q", stub.file, stub.filename)
	}
	if stub.path != "/api/v2/parse/pjb-7f3a" {
		t.Errorf("result path = %q", stub.path)
	}
	// The pages are fetched once, after the job completes, not on each poll.
	if stub.statusCount.Load() != 2 || stub.resultCount.Load() != 1 {
		t.Errorf("status checks = %d, result fetches = %d", stub.statusCount.Load(), stub.resultCount.Load())
	}

	if document.Markdown != "# Dosing guide\n\nGive once daily.\n\n| Drug | Dose |\n|---|---|\n| Amoxicillin | 500 mg |" {
		t.Errorf("markdown = %q", document.Markdown)
	}
	if len(document.Pages) != 2 || document.Pages[0].Number != 1 || document.Pages[1].Number != 3 {
		t.Errorf("pages = %+v", document.Pages)
	}
	if document.PageMarkdown(1) != "| Drug | Dose |\n|---|---|\n| Amoxicillin | 500 mg |" {
		t.Errorf("page 3 = %q", document.PageMarkdown(1))
	}
	// The page LlamaParse could not read is named, not silently missing.
	if len(document.SkippedPages) != 1 || document.SkippedPages[0] != 2 {
		t.Errorf("skipped = %v", document.SkippedPages)
	}
	if document.Parser != parse.LlamaParse {
		t.Errorf("parser = %q", document.Parser)
	}
}

func TestAJobLlamaParseFailsIsParseFailedWithItsReason(t *testing.T) {
	stub := &llamaStub{
		uploads:  []reply{{200, fixture(t, "created.json")}},
		statuses: []reply{{200, fixture(t, "failed.json")}},
	}
	client := start(t, stub, 5*time.Second)

	_, err := client.Parse(context.Background(), parse.StringSource("guide.pdf", "x"))
	if parse.KindOf(err) != parse.ParseFailed || !strings.Contains(err.Error(), "encrypted") {
		t.Fatalf("err = %v", err)
	}
	if stub.uploadCount.Load() != 1 {
		t.Errorf("uploads = %d: a failed job was sent again", stub.uploadCount.Load())
	}
}

func TestWhenEveryPageFailsTheParseFailed(t *testing.T) {
	stub := &llamaStub{
		uploads:  []reply{{200, fixture(t, "created.json")}},
		statuses: []reply{{200, fixture(t, "completed.json")}},
		results: []reply{{200, []byte(`{"job":{"status":"COMPLETED"},"markdown":{"pages":[
			{"page_number":1,"success":false,"error":"Page could not be rendered"}]}}`)}},
	}
	client := start(t, stub, 5*time.Second)

	_, err := client.Parse(context.Background(), parse.StringSource("guide.pdf", "x"))
	if parse.KindOf(err) != parse.ParseFailed || !strings.Contains(err.Error(), "could not be rendered") {
		t.Fatalf("err = %v", err)
	}
}

func TestACompletedJobWithBlankPagesIsNoText(t *testing.T) {
	stub := &llamaStub{
		uploads:  []reply{{200, fixture(t, "created.json")}},
		statuses: []reply{{200, fixture(t, "completed.json")}},
		results:  []reply{{200, []byte(`{"job":{"status":"COMPLETED"},"markdown":{"pages":[{"page_number":1,"success":true,"markdown":"\n"}]}}`)}},
	}
	client := start(t, stub, 5*time.Second)

	_, err := client.Parse(context.Background(), parse.StringSource("guide.pdf", "x"))
	if parse.KindOf(err) != parse.NoText {
		t.Fatalf("err = %v", err)
	}
}

func TestRefusalsOfTheUploadAreTypedAndNeverResent(t *testing.T) {
	cases := []struct {
		name string
		reply
		want parse.Kind
	}{
		{"bad key", reply{401, []byte(`{"detail":"Invalid API Key"}`)}, parse.Unauthorized},
		{"out of credits", reply{402, []byte(`{"detail":"Payment required"}`)}, parse.Unauthorized},
		{"misplaced option", reply{422, []byte(`{"detail":[{"type":"extra_forbidden","loc":["body","max_pages"],"msg":"Extra inputs are not permitted"}]}`)}, parse.Rejected},
		{"rate limited", reply{429, []byte(`{"detail":"Too many requests"}`)}, parse.RateLimited},
		{"server error", reply{503, []byte(`Service Unavailable`)}, parse.Unavailable},
		{"no id", reply{200, []byte(`{"status":"PENDING"}`)}, parse.BadResponse},
	}
	for _, tc := range cases {
		stub := &llamaStub{uploads: []reply{tc.reply}}
		client := start(t, stub, 5*time.Second)
		_, err := client.Parse(context.Background(), parse.StringSource("guide.pdf", "x"))
		if parse.KindOf(err) != tc.want {
			t.Errorf("%s: err = %v", tc.name, err)
		}
		if stub.uploadCount.Load() != 1 {
			t.Errorf("%s: uploads = %d", tc.name, stub.uploadCount.Load())
		}
	}
}

func TestAFailedQuestionAboutTheJobIsAskedAgain(t *testing.T) {
	stub := &llamaStub{
		uploads: []reply{{200, fixture(t, "created.json")}},
		statuses: []reply{
			{502, []byte("Bad Gateway")},
			{429, []byte(`{"detail":"Too many requests"}`)},
			{200, fixture(t, "completed.json")},
		},
		results: []reply{{500, []byte("oops")}, {200, fixture(t, "completed_markdown.json")}},
	}
	client := start(t, stub, 5*time.Second)

	if _, err := client.Parse(context.Background(), parse.StringSource("guide.pdf", "x")); err != nil {
		t.Fatal(err)
	}
	if stub.uploadCount.Load() != 1 || stub.statusCount.Load() != 3 || stub.resultCount.Load() != 2 {
		t.Errorf("uploads = %d, statuses = %d, results = %d",
			stub.uploadCount.Load(), stub.statusCount.Load(), stub.resultCount.Load())
	}
}

func TestAJobCheckRefusedIsFinal(t *testing.T) {
	stub := &llamaStub{
		uploads:  []reply{{200, fixture(t, "created.json")}},
		statuses: []reply{{404, []byte(`{"detail":"Job not found"}`)}},
	}
	client := start(t, stub, 5*time.Second)

	_, err := client.Parse(context.Background(), parse.StringSource("guide.pdf", "x"))
	if parse.KindOf(err) != parse.Rejected || stub.statusCount.Load() != 1 {
		t.Fatalf("err = %v, checks = %d", err, stub.statusCount.Load())
	}
}

func TestAJobThatOutlivesTheTimeoutIsTimedOutAndNotResubmitted(t *testing.T) {
	stub := &llamaStub{
		uploads:  []reply{{200, fixture(t, "created.json")}},
		statuses: []reply{{200, fixture(t, "running.json")}},
	}
	client := start(t, stub, 60*time.Millisecond)

	_, err := client.Parse(context.Background(), parse.StringSource("guide.pdf", "x"))
	var failure *parse.Error
	if !errors.As(err, &failure) || failure.Kind != parse.TimedOut || failure.Retryable() {
		t.Fatalf("err = %v", err)
	}
	if stub.uploadCount.Load() != 1 {
		t.Errorf("uploads = %d", stub.uploadCount.Load())
	}
}

func TestEveryConfigFieldIsRequired(t *testing.T) {
	complete := Config{
		APIKey: "k", BaseURL: "https://api.cloud.llamaindex.ai", Tier: "agentic", Version: "latest",
		PollInterval: time.Second, Timeout: time.Second, MaxResponseBytes: 1,
	}
	if _, err := New(complete); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Config){
		"APIKey":           func(c *Config) { c.APIKey = "" },
		"BaseURL":          func(c *Config) { c.BaseURL = "" },
		"Tier":             func(c *Config) { c.Tier = "premium" },
		"Version":          func(c *Config) { c.Version = "" },
		"PollInterval":     func(c *Config) { c.PollInterval = 0 },
		"Timeout":          func(c *Config) { c.Timeout = 0 },
		"MaxResponseBytes": func(c *Config) { c.MaxResponseBytes = 0 },
	} {
		config := complete
		change(&config)
		if _, err := New(config); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestExtensionsMatchesReads(t *testing.T) {
	client, _ := New(Config{
		APIKey: "k", BaseURL: "https://api.cloud.llamaindex.ai", Tier: "agentic", Version: "latest",
		PollInterval: time.Second, Timeout: time.Second, MaxResponseBytes: 1,
	})
	listed := Extensions()
	if len(listed) != len(extensions) {
		t.Fatalf("Extensions lists %d, Reads accepts %d", len(listed), len(extensions))
	}
	for _, extension := range listed {
		if !client.Reads(extension) {
			t.Errorf("%s is listed and not read", extension)
		}
	}
}
