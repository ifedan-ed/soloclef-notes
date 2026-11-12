package unstructured

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

// fixture is a reply written by hand from Unstructured's documented element
// shape (type, element_id, text, metadata) and its documented 401 body.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	content, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func start(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := New(Config{
		APIKey: "test-key", URL: server.URL + "/general/v0/general", Strategy: "hi_res",
		Timeout: 5 * time.Second, MaxResponseBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestParseSendsOneRequestAndRendersTheElements(t *testing.T) {
	var form map[string][]string
	var file, filename, key, accept, path string
	client := start(t, func(w http.ResponseWriter, r *http.Request) {
		key, accept, path = r.Header.Get("unstructured-api-key"), r.Header.Get("Accept"), r.URL.Path
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("not multipart: %v", err)
		}
		form = r.MultipartForm.Value
		header := r.MultipartForm.File["files"][0]
		opened, _ := header.Open()
		content, _ := io.ReadAll(opened)
		file, filename = string(content), header.Filename
		w.Write(fixture(t, "elements_pdf.json"))
	})

	document, err := client.Parse(context.Background(), parse.StringSource("guide.pdf", "%PDF-bytes"))
	if err != nil {
		t.Fatal(err)
	}

	if key != "test-key" || accept != "application/json" || path != "/general/v0/general" {
		t.Errorf("key=%q accept=%q path=%q", key, accept, path)
	}
	if file != "%PDF-bytes" || filename != "guide.pdf" {
		t.Errorf("file = %q named %q", file, filename)
	}
	if len(form) != 2 || form["strategy"][0] != "hi_res" || form["output_format"][0] != "application/json" {
		t.Errorf("form = %v", form)
	}
	// One chunker for every parser: the API is never asked to chunk.
	if _, asked := form["chunking_strategy"]; asked {
		t.Error("chunking_strategy was sent")
	}

	pageOne := "# Dosing guide\n\n" +
		"Give once daily unless the table says otherwise.\n\n" +
		"- Check allergies\n- Check renal function"
	pageTwo := "## Antibiotics\n\n" +
		"| Drug | Dose |\n|---|---|\n| Amoxicillin | 500 mg |\n\n" +
		"$$\ndose = weight * 25\n$$"
	if document.Markdown != pageOne+"\n\n"+pageTwo {
		t.Errorf("markdown:\n%s", document.Markdown)
	}
	if len(document.Pages) != 2 || document.Pages[0].Number != 1 || document.Pages[1].Number != 2 {
		t.Fatalf("pages = %+v", document.Pages)
	}
	if document.PageMarkdown(0) != pageOne || document.PageMarkdown(1) != pageTwo {
		t.Errorf("page 1 = %q\npage 2 = %q", document.PageMarkdown(0), document.PageMarkdown(1))
	}
	// The running header, the footer and the page number are not content.
	for _, furniture := range []string{"Ward formulary 2026", "Printed copies", "\n1\n"} {
		if strings.Contains(document.Markdown, furniture) {
			t.Errorf("page furniture %q was kept", furniture)
		}
	}
	if document.Parser != parse.Unstructured {
		t.Errorf("parser = %q", document.Parser)
	}
}

func TestAFormatWithoutPagesIsUnpaged(t *testing.T) {
	client := start(t, func(w http.ResponseWriter, r *http.Request) { w.Write(fixture(t, "elements_email.json")) })

	document, err := client.Parse(context.Background(), parse.StringSource("move.eml", "From: ada"))
	if err != nil {
		t.Fatal(err)
	}
	want := "The clinic moves to the second floor on Monday.\n\n```\nroom = 2.14\n```"
	if document.Pages != nil || document.Markdown != want {
		t.Fatalf("document = %+v", document)
	}
}

func TestATableWithoutHTMLKeepsItsText(t *testing.T) {
	client := start(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"type":"Table","element_id":"1","text":"Drug Dose Amoxicillin 500 mg","metadata":{"page_number":1}}]`))
	})
	document, err := client.Parse(context.Background(), parse.StringSource("a.pdf", "x"))
	if err != nil || document.Markdown != "Drug Dose Amoxicillin 500 mg" {
		t.Fatalf("document = %+v, err = %v", document, err)
	}
}

func TestATypeAddedLaterIsKeptAsAParagraph(t *testing.T) {
	client := start(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"type":"Sidebar","element_id":"1","text":"See also chapter 4.","metadata":{"page_number":1}}]`))
	})
	document, err := client.Parse(context.Background(), parse.StringSource("a.pdf", "x"))
	if err != nil || document.Markdown != "See also chapter 4." {
		t.Fatalf("document = %+v, err = %v", document, err)
	}
}

func TestNoElementsWithTextIsNoText(t *testing.T) {
	for name, body := range map[string]string{
		"empty array":    `[]`,
		"furniture only": `[{"type":"Footer","element_id":"1","text":"page 1","metadata":{"page_number":1}}]`,
	} {
		client := start(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) })
		_, err := client.Parse(context.Background(), parse.StringSource("a.pdf", "x"))
		if parse.KindOf(err) != parse.NoText {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestFailuresAreTypedAndTheRequestIsNeverRepeated(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   []byte
		want   parse.Kind
		retry  bool
	}{
		{"missing key", 401, fixture(t, "missing_key.json"), parse.Unauthorized, false},
		{"validation", 422, []byte(`{"detail":"Invalid strategy"}`), parse.Rejected, false},
		{"rate limited", 429, []byte(`{"detail":"Too many requests"}`), parse.RateLimited, true},
		{"server error", 500, []byte(`{"detail":"Internal Server Error"}`), parse.Unavailable, true},
		{"an object, not the array", 200, []byte(`{"detail":"moved"}`), parse.BadResponse, false},
		{"cut short", 200, []byte(`[{"type":"Title","text":"Dos`), parse.BadResponse, false},
	}
	for _, tc := range cases {
		requests := 0
		client := start(t, func(w http.ResponseWriter, r *http.Request) {
			requests++
			w.WriteHeader(tc.status)
			w.Write(tc.body)
		})
		_, err := client.Parse(context.Background(), parse.StringSource("a.pdf", "x"))
		var failure *parse.Error
		if !errors.As(err, &failure) || failure.Kind != tc.want || failure.Retryable() != tc.retry {
			t.Errorf("%s: err = %v", tc.name, err)
		}
		if requests != 1 {
			t.Errorf("%s: %d requests", tc.name, requests)
		}
	}
}

func TestAReplyOverTheLimitIsRefused(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"type":"NarrativeText","text":"` + strings.Repeat("x", 4096) + `","metadata":{}}]`))
	}))
	defer server.Close()
	client, _ := New(Config{APIKey: "k", URL: server.URL, Strategy: "fast", Timeout: 5 * time.Second, MaxResponseBytes: 1024})

	_, err := client.Parse(context.Background(), parse.StringSource("a.pdf", "x"))
	if parse.KindOf(err) != parse.BadResponse || !strings.Contains(err.Error(), "1024 bytes") {
		t.Fatalf("err = %v", err)
	}
}

func TestARequestThatOutlivesTheTimeoutIsTimedOut(t *testing.T) {
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
	client, _ := New(Config{APIKey: "k", URL: server.URL, Strategy: "fast", Timeout: 50 * time.Millisecond, MaxResponseBytes: 1024})

	_, err := client.Parse(context.Background(), parse.StringSource("a.pdf", "x"))
	if parse.KindOf(err) != parse.TimedOut {
		t.Fatalf("err = %v", err)
	}
}

func TestEveryConfigFieldIsRequired(t *testing.T) {
	complete := Config{APIKey: "k", URL: "https://api.unstructuredapp.io/general/v0/general", Strategy: "auto", Timeout: time.Second, MaxResponseBytes: 1}
	if _, err := New(complete); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Config){
		"APIKey":           func(c *Config) { c.APIKey = "" },
		"URL":              func(c *Config) { c.URL = "" },
		"Strategy":         func(c *Config) { c.Strategy = "best" },
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
	client, _ := New(Config{APIKey: "k", URL: "https://api.unstructuredapp.io/general/v0/general", Strategy: "auto", Timeout: time.Second, MaxResponseBytes: 1})
	listed := Extensions()
	if len(listed) != len(extensions) {
		t.Fatalf("Extensions lists %d, Reads accepts %d", len(listed), len(extensions))
	}
	for _, extension := range listed {
		if !client.Reads(extension) {
			t.Errorf("%s is listed and not read", extension)
		}
	}
	if client.Reads(".html") {
		t.Error(".html is read in Go, not sent to Unstructured")
	}
}
