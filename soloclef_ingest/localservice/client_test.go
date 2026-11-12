package localservice

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

// fixture is a reply written by hand from the service's documented shapes
// (soloclef_parse_service/parse_service/http_api.py).
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	content, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func client(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	c, err := New(Config{BaseURL: server.URL, Timeout: 5 * time.Second, MaxResponseBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestParseStreamsTheFileAndReadsThePagesBack(t *testing.T) {
	var gotPath, gotName, gotBody string
	var gotLength int64
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotName, gotLength = r.URL.Path, r.URL.Query().Get("filename"), r.ContentLength
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Write(fixture(t, "parse_ok.json"))
	})

	document, err := c.Parse(context.Background(), parse.StringSource("Dosing guide (v2).pdf", "%PDF-bytes"))
	if err != nil {
		t.Fatal(err)
	}

	if gotPath != "/v1/parse" || gotName != "Dosing guide (v2).pdf" || gotBody != "%PDF-bytes" || gotLength != 10 {
		t.Errorf("request: path=%q name=%q body=%q length=%d", gotPath, gotName, gotBody, gotLength)
	}
	wantMarkdown := "# Dosing guide \n\nGive once daily. \n\n|Drug|Dose|\n|---|---|\n|Amoxicillin|500 mg|\n\n"
	if document.Markdown != wantMarkdown {
		t.Errorf("markdown = %q", document.Markdown)
	}
	if len(document.Pages) != 2 || document.Pages[1].Number != 2 ||
		document.PageMarkdown(1) != "|Drug|Dose|\n|---|---|\n|Amoxicillin|500 mg|\n\n" {
		t.Errorf("pages = %+v", document.Pages)
	}
	if document.Parser != parse.Local {
		t.Errorf("parser = %q", document.Parser)
	}
}

func TestEachRefusalKeepsItsKindAndTheServicesWords(t *testing.T) {
	cases := []struct {
		fixture string
		status  int
		want    parse.Kind
		says    string
	}{
		{"refused_no_text_layer.json", 422, parse.NoText, "looks like a scan"},
		{"refused_parse_error.json", 422, parse.ParseFailed, "FileDataError"},
		{"refused_unsupported_format.json", 415, parse.UnsupportedFormat, ".xlsx is not read here"},
		{"busy.json", 503, parse.Unavailable, "Nothing was started"},
	}
	for _, tc := range cases {
		c := client(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			w.Write(fixture(t, tc.fixture))
		})
		_, err := c.Parse(context.Background(), parse.StringSource("a.pdf", "x"))
		var failure *parse.Error
		if !errors.As(err, &failure) || failure.Kind != tc.want || failure.Status != tc.status ||
			!strings.Contains(failure.Message, tc.says) {
			t.Errorf("%s: err = %v", tc.fixture, err)
		}
	}
}

func TestOnlyABusyOrAbsentServiceIsRetryable(t *testing.T) {
	busy := client(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		w.Write(fixture(t, "busy.json"))
	})
	_, err := busy.Parse(context.Background(), parse.StringSource("a.pdf", "x"))
	var failure *parse.Error
	if !errors.As(err, &failure) || !failure.Retryable() {
		t.Fatalf("busy: err = %v", err)
	}

	// Nothing listens here: the service is down.
	gone, _ := New(Config{BaseURL: "http://127.0.0.1:1", Timeout: 5 * time.Second, MaxResponseBytes: 1 << 20})
	_, err = gone.Parse(context.Background(), parse.StringSource("a.pdf", "x"))
	if !errors.As(err, &failure) || failure.Kind != parse.Unavailable || !failure.Retryable() {
		t.Fatalf("down: err = %v", err)
	}
}

func TestAGatewayErrorPageIsTypedByItsStatus(t *testing.T) {
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		w.Write([]byte("<html>Bad Gateway</html>"))
	})
	_, err := c.Parse(context.Background(), parse.StringSource("a.pdf", "x"))
	if parse.KindOf(err) != parse.Unavailable {
		t.Fatalf("err = %v", err)
	}
}

func TestAPagesReplyWithNoTextIsNoText(t *testing.T) {
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"pages":[{"number":1,"markdown":"  \n"}]}`))
	})
	_, err := c.Parse(context.Background(), parse.StringSource("a.pdf", "x"))
	if parse.KindOf(err) != parse.NoText {
		t.Fatalf("err = %v", err)
	}
}

func TestAReplyThatIsNotTheContractIsABadResponse(t *testing.T) {
	for name, body := range map[string]string{
		"not json":       "<html>",
		"no pages":       `{"parser":"pymupdf4llm"}`,
		"pages a string": `{"pages":"# text"}`,
		"cut short":      `{"pages":[{"number":1,"markdown":"abc`,
	} {
		c := client(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) })
		_, err := c.Parse(context.Background(), parse.StringSource("a.pdf", "x"))
		if parse.KindOf(err) != parse.BadResponse {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestMarkdownOverTheLimitIsRefused(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"pages":[{"number":1,"markdown":"` + strings.Repeat("x", 4096) + `"}]}`))
	}))
	defer server.Close()
	c, _ := New(Config{BaseURL: server.URL, Timeout: 5 * time.Second, MaxResponseBytes: 1024})

	_, err := c.Parse(context.Background(), parse.StringSource("a.pdf", "x"))
	if parse.KindOf(err) != parse.BadResponse || !strings.Contains(err.Error(), "1024 bytes") {
		t.Fatalf("err = %v", err)
	}
}

func TestAParseThatOutlivesTheTimeoutIsTimedOut(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)
	c, _ := New(Config{BaseURL: server.URL, Timeout: 50 * time.Millisecond, MaxResponseBytes: 1024})

	_, err := c.Parse(context.Background(), parse.StringSource("a.pdf", "x"))
	if parse.KindOf(err) != parse.TimedOut {
		t.Fatalf("err = %v", err)
	}
}

func TestHealth(t *testing.T) {
	healthy := client(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Write([]byte(`{"status":"ok","parser":"pymupdf4llm","parser_version":"1.28.2","workers":2,"workers_alive":2,"workers_idle":2}`))
	})
	if err := healthy.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	degraded := client(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		w.Write([]byte(`{"status":"degraded","workers":2,"workers_alive":1}`))
	})
	if err := degraded.Health(context.Background()); parse.KindOf(err) != parse.Unavailable {
		t.Fatalf("err = %v", err)
	}
}

func TestItReadsTheServicesFourFormats(t *testing.T) {
	c := client(t, func(http.ResponseWriter, *http.Request) {})
	for extension, want := range map[string]bool{".pdf": true, ".docx": true, ".pptx": true, ".epub": true, ".xlsx": false, ".png": false} {
		if c.Reads(extension) != want {
			t.Errorf("Reads(%q) = %v", extension, !want)
		}
	}
}

func TestEveryConfigFieldIsRequired(t *testing.T) {
	complete := Config{BaseURL: "http://parse-service:8080", Timeout: time.Second, MaxResponseBytes: 1}
	if _, err := New(complete); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Config){
		"BaseURL":          func(c *Config) { c.BaseURL = "parse-service:8080" },
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
