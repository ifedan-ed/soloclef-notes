package jmapmail

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The JMAP tests run against recorded fixtures: the replies a real
// Stalwart (stalwartlabs/stalwart:v1.0, in a throwaway container) gave to
// the exact requests this package makes. To record them again, start such
// a server with two users holding the test mail (see the end-to-end test)
// and run
//
//	JMAPMAIL_RECORD=http://127.0.0.1:PORT \
//	JMAPMAIL_RECORD_READER='ben@example.test:password' \
//	JMAPMAIL_RECORD_SENDER='ada@example.test:password' \
//	  go test ./jmapmail/ -run Fixture -count=1
//
// A fixture holds the status, a Location header and the reply's body;
// never a request's credentials.

// fixtureOrigin is the server address every fixture test names. Recording
// rewrites it to the real server; replaying never leaves the process.
const fixtureOrigin = "https://mail.example.test"

type exchangeFixture struct {
	Status   int             `json:"status"`
	Location string          `json:"location,omitempty"`
	Body     json.RawMessage `json:"body,omitempty"`
	// Text is a body that is not JSON (a downloaded attachment).
	Text string `json:"text,omitempty"`
}

// sentRequest is one request a test made, for its assertions.
type sentRequest struct {
	Method, Path  string
	Authorization string
	Body          []byte
}

// fixtures is the Network of a fixture test.
type fixtures struct {
	t      *testing.T
	name   string
	target *url.URL
	mu     sync.Mutex
	count  int
	sent   []sentRequest
}

func newFixtures(t *testing.T, name string) *fixtures {
	f := &fixtures{t: t, name: name}
	if address := os.Getenv("JMAPMAIL_RECORD"); address != "" {
		target, err := url.Parse(address)
		if err != nil {
			t.Fatal(err)
		}
		f.target = target
	}
	return f
}

// credential is who a recording signs in as; replaying needs none, and
// any credential is sent so that the header is there to be checked.
func (f *fixtures) credential(role string) Credential {
	pair := os.Getenv("JMAPMAIL_RECORD_" + strings.ToUpper(role))
	if f.target == nil || pair == "" {
		return Credential{Username: role + "@example.test", Password: "not-a-real-password"}
	}
	user, password, _ := strings.Cut(pair, ":")
	return Credential{Username: user, Password: password}
}

func (f *fixtures) Client(string) (*http.Client, error) {
	return &http.Client{Transport: f, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

func (f *fixtures) RoundTrip(request *http.Request) (*http.Response, error) {
	var body []byte
	if request.Body != nil {
		body, _ = io.ReadAll(request.Body)
	}
	f.mu.Lock()
	f.count++
	number := f.count
	f.sent = append(f.sent, sentRequest{Method: request.Method, Path: request.URL.RequestURI(),
		Authorization: request.Header.Get("Authorization"), Body: body})
	f.mu.Unlock()
	file := filepath.Join("testdata", fmt.Sprintf("%s_%02d.json", f.name, number))
	var held exchangeFixture
	if f.target != nil {
		forwarded := request.Clone(request.Context())
		forwarded.URL.Scheme, forwarded.URL.Host, forwarded.Host = f.target.Scheme, f.target.Host, f.target.Host
		forwarded.RequestURI = ""
		forwarded.Body = io.NopCloser(bytes.NewReader(body))
		response, err := http.DefaultTransport.RoundTrip(forwarded)
		if err != nil {
			return nil, err
		}
		raw, _ := io.ReadAll(response.Body)
		response.Body.Close()
		held = exchangeFixture{Status: response.StatusCode, Location: response.Header.Get("Location")}
		switch {
		case len(raw) == 0:
		case json.Valid(raw):
			held.Body = raw
		default:
			held.Text = string(raw)
		}
		encoded, _ := json.MarshalIndent(held, "", "  ")
		if err := os.WriteFile(file, append(encoded, '\n'), 0o644); err != nil {
			f.t.Fatal(err)
		}
	} else {
		raw, err := os.ReadFile(file)
		if err != nil {
			f.t.Fatalf("no fixture %s for request %d (%s %s): %v", file, number, request.Method, request.URL.Path, err)
		}
		if err := json.Unmarshal(raw, &held); err != nil {
			f.t.Fatalf("fixture %s: %v", file, err)
		}
	}
	header := http.Header{"Content-Type": {"application/json"}}
	if held.Location != "" {
		header.Set("Location", held.Location)
	}
	payload := []byte(held.Body)
	if held.Text != "" {
		payload = []byte(held.Text)
	}
	return &http.Response{StatusCode: held.Status, Header: header, Body: io.NopCloser(bytes.NewReader(payload)), Request: request}, nil
}

// calls is the method calls of the n-th request (from 1), by call id.
func (f *fixtures) calls(n int) map[string][]json.RawMessage {
	f.t.Helper()
	var envelope struct {
		Using       []string            `json:"using"`
		MethodCalls [][]json.RawMessage `json:"methodCalls"`
	}
	if err := json.Unmarshal(f.sent[n-1].Body, &envelope); err != nil {
		f.t.Fatalf("request %d is not a JMAP request: %v", n, err)
	}
	out := map[string][]json.RawMessage{}
	for _, call := range envelope.MethodCalls {
		var id string
		_ = json.Unmarshal(call[2], &id)
		out[id] = call
	}
	return out
}
