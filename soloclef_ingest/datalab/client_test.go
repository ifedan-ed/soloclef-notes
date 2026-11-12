package datalab

import (
	"context"
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

// fixture is a reply written by hand from Datalab's documented schemas
// (InitialResponse, MarkerFinalResponse, HTTPValidationError).
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	content, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

// reply is one scripted answer.
type reply struct {
	status int
	body   []byte
}

// datalabStub answers uploads and status checks from two scripts, in order;
// the last entry of each repeats. It records what it was sent.
type datalabStub struct {
	t       *testing.T
	uploads []reply
	checks  []reply

	uploadCount, checkCount atomic.Int32
	lastUpload              *http.Request
	lastForm                map[string]string
	lastFile                string
	lastFilename            string
	checkPaths              []string
}

func (s *datalabStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if got := r.Header.Get("X-API-Key"); got != "test-key" {
		s.t.Errorf("X-API-Key = %q", got)
	}
	var script []reply
	var index int32
	if r.Method == http.MethodPost {
		if r.ContentLength <= 0 {
			s.t.Errorf("the upload has no Content-Length (%d)", r.ContentLength)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			s.t.Errorf("the upload is not multipart: %v", err)
		}
		s.lastUpload = r
		s.lastForm = map[string]string{}
		for name, values := range r.MultipartForm.Value {
			s.lastForm[name] = values[0]
		}
		header := r.MultipartForm.File["file"][0]
		file, _ := header.Open()
		content, _ := io.ReadAll(file)
		s.lastFile, s.lastFilename = string(content), header.Filename
		script, index = s.uploads, s.uploadCount.Add(1)-1
	} else {
		s.checkPaths = append(s.checkPaths, r.URL.Path)
		script, index = s.checks, s.checkCount.Add(1)-1
	}
	answer := script[min(int(index), len(script)-1)]
	w.WriteHeader(answer.status)
	w.Write(answer.body)
}

func start(t *testing.T, stub *datalabStub, timeout time.Duration) *Client {
	t.Helper()
	stub.t = t
	server := httptest.NewServer(stub)
	t.Cleanup(server.Close)
	client, err := New(Config{
		APIKey: "test-key", Endpoint: server.URL + "/api/v1/convert", Mode: "balanced",
		PollInterval: time.Millisecond, RateLimitWait: time.Millisecond,
		Timeout: timeout, MaxResponseBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestParseUploadsPollsAndSplitsThePages(t *testing.T) {
	stub := &datalabStub{
		uploads: []reply{{200, fixture(t, "submitted.json")}},
		checks:  []reply{{200, fixture(t, "processing.json")}, {200, fixture(t, "complete.json")}},
	}
	client := start(t, stub, 5*time.Second)

	document, err := client.Parse(context.Background(), parse.StringSource("guide.pdf", "%PDF-bytes"))
	if err != nil {
		t.Fatal(err)
	}

	wantForm := map[string]string{
		"output_format": "markdown", "mode": "balanced", "paginate": "true", "disable_image_extraction": "true",
	}
	for name, want := range wantForm {
		if stub.lastForm[name] != want {
			t.Errorf("form %s = %q, want %q", name, stub.lastForm[name], want)
		}
	}
	if len(stub.lastForm) != len(wantForm) {
		t.Errorf("form = %v", stub.lastForm)
	}
	if stub.lastFile != "%PDF-bytes" || stub.lastFilename != "guide.pdf" {
		t.Errorf("file = %q named %q", stub.lastFile, stub.lastFilename)
	}
	// The status is asked for at the documented path under the configured
	// endpoint. The fixture's request_check_url names another host; had it
	// been followed, this stub would have seen no check at all.
	if stub.checkPaths[0] != "/api/v1/convert/req_8b1f2c3d" {
		t.Errorf("check path = %q", stub.checkPaths[0])
	}

	if document.Markdown != "# Dosing guide\n\nGive once daily.\n\n| Drug | Dose |\n|---|---|\n| Amoxicillin | 500 mg |" {
		t.Errorf("markdown = %q", document.Markdown)
	}
	if len(document.Pages) != 2 || document.Pages[0].Number != 1 || document.Pages[1].Number != 2 {
		t.Fatalf("pages = %+v", document.Pages)
	}
	if document.PageMarkdown(0) != "# Dosing guide\n\nGive once daily." {
		t.Errorf("page 1 = %q", document.PageMarkdown(0))
	}
	if document.Parser != parse.Datalab {
		t.Errorf("parser = %q", document.Parser)
	}
}

func TestARateLimitedUploadIsSentAgainFromStorage(t *testing.T) {
	stub := &datalabStub{
		uploads: []reply{{429, []byte(`{"detail":"Rate limit exceeded"}`)}, {200, fixture(t, "submitted.json")}},
		checks:  []reply{{200, fixture(t, "complete.json")}},
	}
	client := start(t, stub, 5*time.Second)
	source := parse.StringSource("guide.pdf", "%PDF-bytes")
	opens := 0
	open := source.Open
	source.Open = func(ctx context.Context) (io.ReadCloser, error) { opens++; return open(ctx) }

	if _, err := client.Parse(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	if stub.uploadCount.Load() != 2 || opens != 2 {
		t.Errorf("uploads = %d, opens = %d", stub.uploadCount.Load(), opens)
	}
	if stub.lastFile != "%PDF-bytes" {
		t.Errorf("the second upload carried %q", stub.lastFile)
	}
}

func TestAnUploadStillRateLimitedAfterThreeTriesFails(t *testing.T) {
	stub := &datalabStub{uploads: []reply{{429, []byte(`{"detail":"Rate limit exceeded"}`)}}}
	client := start(t, stub, 5*time.Second)

	_, err := client.Parse(context.Background(), parse.StringSource("guide.pdf", "x"))
	var failure *parse.Error
	if !errors.As(err, &failure) || failure.Kind != parse.RateLimited || !failure.Retryable() {
		t.Fatalf("err = %v", err)
	}
	if stub.uploadCount.Load() != 3 {
		t.Errorf("uploads = %d", stub.uploadCount.Load())
	}
}

func TestAServerErrorOnUploadIsNotSentAgain(t *testing.T) {
	// Whether a conversion started is unknown, and a second one is billed.
	stub := &datalabStub{uploads: []reply{{502, []byte("Bad Gateway")}}}
	client := start(t, stub, 5*time.Second)

	_, err := client.Parse(context.Background(), parse.StringSource("guide.pdf", "x"))
	if parse.KindOf(err) != parse.Unavailable || stub.uploadCount.Load() != 1 {
		t.Fatalf("err = %v, uploads = %d", err, stub.uploadCount.Load())
	}
}

func TestRefusalsOfTheUploadAreTyped(t *testing.T) {
	cases := []struct {
		name string
		reply
		want parse.Kind
	}{
		{"bad key", reply{401, []byte(`{"detail":"Invalid API key"}`)}, parse.Unauthorized},
		{"out of credit", reply{402, []byte(`{"detail":"Payment required"}`)}, parse.Unauthorized},
		{"validation", reply{422, fixture(t, "validation_error.json")}, parse.Rejected},
		{"success false", reply{200, []byte(`{"success":false,"error":"Unsupported file type","request_id":"","request_check_url":""}`)}, parse.Rejected},
		{"no request id", reply{200, []byte(`{"success":true}`)}, parse.BadResponse},
	}
	for _, tc := range cases {
		stub := &datalabStub{uploads: []reply{tc.reply}}
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
	stub := &datalabStub{
		uploads: []reply{{200, fixture(t, "submitted.json")}},
		checks: []reply{
			{502, []byte("Bad Gateway")},
			{429, []byte(`{"detail":"Rate limit exceeded"}`)},
			{200, fixture(t, "complete.json")},
		},
	}
	client := start(t, stub, 5*time.Second)

	if _, err := client.Parse(context.Background(), parse.StringSource("guide.pdf", "x")); err != nil {
		t.Fatal(err)
	}
	if stub.uploadCount.Load() != 1 || stub.checkCount.Load() != 3 {
		t.Errorf("uploads = %d, checks = %d", stub.uploadCount.Load(), stub.checkCount.Load())
	}
}

func TestThePageLimitEndsTheRequestAsRateLimited(t *testing.T) {
	stub := &datalabStub{
		uploads: []reply{{200, fixture(t, "submitted.json")}},
		checks:  []reply{{429, []byte(`{"detail":"Page rate limit exceeded. Wait for in-flight requests to finish."}`)}},
	}
	client := start(t, stub, 5*time.Second)

	_, err := client.Parse(context.Background(), parse.StringSource("guide.pdf", "x"))
	if parse.KindOf(err) != parse.RateLimited || stub.checkCount.Load() != 1 {
		t.Fatalf("err = %v, checks = %d", err, stub.checkCount.Load())
	}
}

func TestAConversionDatalabFailsIsParseFailedWithItsReason(t *testing.T) {
	stub := &datalabStub{
		uploads: []reply{{200, fixture(t, "submitted.json")}},
		checks:  []reply{{200, fixture(t, "failed.json")}},
	}
	client := start(t, stub, 5*time.Second)

	_, err := client.Parse(context.Background(), parse.StringSource("guide.pdf", "x"))
	if parse.KindOf(err) != parse.ParseFailed || !strings.Contains(err.Error(), "password protected") {
		t.Fatalf("err = %v", err)
	}
}

func TestAnExpiredResultIsFinal(t *testing.T) {
	stub := &datalabStub{
		uploads: []reply{{200, fixture(t, "submitted.json")}},
		checks:  []reply{{404, []byte(`{"detail":"Not found"}`)}},
	}
	client := start(t, stub, 5*time.Second)

	_, err := client.Parse(context.Background(), parse.StringSource("guide.pdf", "x"))
	if parse.KindOf(err) != parse.Rejected || stub.checkCount.Load() != 1 {
		t.Fatalf("err = %v, checks = %d", err, stub.checkCount.Load())
	}
}

func TestAJobThatOutlivesTheTimeoutIsTimedOutAndNotResubmitted(t *testing.T) {
	stub := &datalabStub{
		uploads: []reply{{200, fixture(t, "submitted.json")}},
		checks:  []reply{{200, fixture(t, "processing.json")}},
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

func TestACancelledParseReturnsTheCallersCancellation(t *testing.T) {
	stub := &datalabStub{
		uploads: []reply{{200, fixture(t, "submitted.json")}},
		checks:  []reply{{200, fixture(t, "processing.json")}},
	}
	client := start(t, stub, 5*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel)

	_, err := client.Parse(ctx, parse.StringSource("guide.pdf", "x"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestEveryConfigFieldIsRequired(t *testing.T) {
	complete := Config{
		APIKey: "k", Endpoint: Endpoint, Mode: "fast", PollInterval: time.Second,
		RateLimitWait: time.Second, Timeout: time.Second, MaxResponseBytes: 1,
	}
	if _, err := New(complete); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Config){
		"APIKey":           func(c *Config) { c.APIKey = "" },
		"Endpoint":         func(c *Config) { c.Endpoint = "" },
		"Mode":             func(c *Config) { c.Mode = "best" },
		"PollInterval":     func(c *Config) { c.PollInterval = 0 },
		"RateLimitWait":    func(c *Config) { c.RateLimitWait = 0 },
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
		APIKey: "k", Endpoint: Endpoint, Mode: "fast", PollInterval: time.Second,
		RateLimitWait: time.Second, Timeout: time.Second, MaxResponseBytes: 1,
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
	if client.Reads(".csv") {
		t.Error(".csv is read in Go, not sent to Datalab")
	}
}
