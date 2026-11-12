package ownmodels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
)

// The two accounts every test here is about. Nothing one of them stored may
// open, list or call as the other.
const (
	ada = "aaaaaaaa-0000-4000-8000-000000000001"
	ben = "bbbbbbbb-0000-4000-8000-000000000002"
)

func newSeal(t *testing.T) KeySeal {
	t.Helper()
	seal, err := NewKeySeal("fake-deployment-secret")
	if err != nil {
		t.Fatal(err)
	}
	return seal
}

func TestAChoiceHasOneSpelling(t *testing.T) {
	if Choice(42) != "own:42" {
		t.Fatalf("Choice(42) = %q", Choice(42))
	}
	if id, own := ParseChoice("own:42"); !own || id != 42 {
		t.Fatalf("ParseChoice(own:42) = %d, %v", id, own)
	}
	// A proxy name, and every other spelling of a row, is not an own
	// model: two strings must never be one choice.
	for _, name := range []string{"gpt-4o", "own:", "own:042", "own:+4", "own:-1", "own:0", "own:4x", "OWN:4", " own:4"} {
		if _, own := ParseChoice(name); own {
			t.Errorf("ParseChoice(%q) read an own model", name)
		}
	}
}

func TestHeadersAreCheckedSealedAndBoundToTheirOwner(t *testing.T) {
	refused := map[string]map[string]string{
		"set by Soloclef": {"authorization": "Bearer x"},
		"given twice":     {"X-Org": "a", "x-org": "b"},
		"letters, digits": {"X Org": "a"},
		"line break":      {"X-Org": "a\r\nX-Injected: b"},
	}
	for want, headers := range refused {
		if got := CheckHeaders(headers); !strings.Contains(got, want) {
			t.Errorf("CheckHeaders(%v) = %q, want a sentence with %q", headers, got, want)
		}
	}
	if got := CheckHeaders(map[string]string{"X-Org": "org-1", "api-key": "fake-header-key"}); got != "" {
		t.Fatalf("good headers refused: %q", got)
	}

	seal := newSeal(t)
	stored, err := SealedHeaders(seal, ada, map[string]string{"x-org": "org-1", "Api-Key": "fake-header-key"})
	if err != nil {
		t.Fatal(err)
	}
	// The value is not in what is stored; the names are, canonical and
	// sorted, for the settings screen.
	if strings.Contains(string(stored), "fake-header-key") || strings.Contains(string(stored), "org-1") {
		t.Fatalf("a header value is stored in the clear: %s", stored)
	}
	if names := HeaderNamesOf(stored); fmt.Sprint(names) != "[Api-Key X-Org]" {
		t.Fatalf("names = %v", names)
	}
	opened, err := OpenHeaders(seal, ada, SealedHeadersOf(stored))
	if err != nil || opened["X-Org"] != "org-1" || opened["Api-Key"] != "fake-header-key" {
		t.Fatalf("opened = %v, %v", opened, err)
	}
	// Copied onto Ben's row, Ada's headers do not open.
	if _, err := OpenHeaders(seal, ben, SealedHeadersOf(stored)); !errors.Is(err, ErrSealUnreadable) {
		t.Fatalf("another account opened the headers: %v", err)
	}
	if empty, _ := SealedHeaders(seal, ada, nil); string(empty) != `{}` {
		t.Fatalf("no headers = %s", empty)
	}
}

func TestAnAddressMustBeAPlainWebAddress(t *testing.T) {
	for base, want := range map[string]string{
		"":                                 "needs the endpoint's base URL",
		"ftp://models.example/v1":          "http:// or https://",
		"https://user:fake@models.example": "key field",
		"https://models.example/v1?x=1":    "no ? or # part",
		"http:///v1":                       "no host",
	} {
		_, err := CheckAddress(base)
		var refused *Refusal
		if !errors.As(err, &refused) || !strings.Contains(refused.Reason, want) {
			t.Errorf("CheckAddress(%q) = %v, want a refusal with %q", base, err, want)
		}
	}
	if got, err := CheckAddress(" https://models.example/v1/ "); err != nil || got != "https://models.example/v1" {
		t.Fatalf("CheckAddress = %q, %v", got, err)
	}
}

// resolver answers the hosts it is given and counts the questions.
type resolver struct {
	mu      sync.Mutex
	answers map[string][]netip.Addr
	asked   int
}

func (r *resolver) resolve(_ context.Context, host string) ([]netip.Addr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.asked++
	if found, ok := r.answers[host]; ok {
		return found, nil
	}
	return nil, errors.New("no such host")
}

// egress is the guard with a fixed allow-list and a resolver of the test's.
func egress(names *resolver, allowed ...string) Egress {
	return Egress{
		Resolve:        names.resolve,
		AllowedHosts:   func(context.Context) ([]string, error) { return allowed, nil },
		ConnectTimeout: 2 * time.Second,
	}
}

// get asks the client for base+"/models".
func get(t *testing.T, e Egress, base string) (*http.Response, error) {
	t.Helper()
	client, err := e.Client(context.Background(), base)
	if err != nil {
		return nil, err
	}
	request, _ := http.NewRequest(http.MethodGet, base+"/models", nil)
	response, err := client.Do(request)
	if err == nil {
		t.Cleanup(func() { response.Body.Close() })
	}
	return response, err
}

func TestTheGuardRefusesThisServersNetworkUnlessTheOperatorAllowsTheHost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer server.Close()
	address, _ := url.Parse(server.URL)
	port := address.Port()
	loopback := netip.MustParseAddr("127.0.0.1")

	t.Run("a loopback address, typed as a number, is refused", func(t *testing.T) {
		_, err := get(t, egress(&resolver{}), server.URL)
		if !IsRefusal(err) {
			t.Fatalf("err = %v, want the guard's refusal", err)
		}
	})
	t.Run("a public-looking name that resolves inside is refused when it connects", func(t *testing.T) {
		names := &resolver{answers: map[string][]netip.Addr{"models.example": {netip.MustParseAddr("10.0.0.5")}}}
		_, err := get(t, egress(names), "http://models.example:"+port)
		if !IsRefusal(err) {
			t.Fatalf("err = %v, want the guard's refusal", err)
		}
		if sentence := RefusalSentence(err, "http://models.example:"+port); !strings.Contains(sentence, "models.example points inside this server's network") {
			t.Fatalf("sentence = %q", sentence)
		}
	})
	t.Run("one private answer among public ones refuses the name", func(t *testing.T) {
		names := &resolver{answers: map[string][]netip.Addr{"mixed.example": {netip.MustParseAddr("8.8.8.8"), loopback}}}
		if _, err := get(t, egress(names), "http://mixed.example:"+port); !IsRefusal(err) {
			t.Fatalf("err = %v, want the guard's refusal", err)
		}
	})
	t.Run("an allow-listed host may be inside", func(t *testing.T) {
		names := &resolver{answers: map[string][]netip.Addr{"ollama.lan": {loopback}}}
		response, err := get(t, egress(names, "OLLAMA.lan."), "http://ollama.lan:"+port)
		if err != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("response = %v, %v", response, err)
		}
	})
	t.Run("the allow-list is read for every client, so a removed host is refused at once", func(t *testing.T) {
		names := &resolver{answers: map[string][]netip.Addr{"ollama.lan": {loopback}}}
		list := []string{"ollama.lan"}
		e := Egress{Resolve: names.resolve, ConnectTimeout: time.Second,
			AllowedHosts: func(context.Context) ([]string, error) { return list, nil }}
		if _, err := get(t, e, "http://ollama.lan:"+port); err != nil {
			t.Fatalf("allowed host refused: %v", err)
		}
		list = nil
		if _, err := get(t, e, "http://ollama.lan:"+port); !IsRefusal(err) {
			t.Fatalf("err = %v after the host was removed, want the guard's refusal", err)
		}
	})
	t.Run("the name is resolved once per connection, so a changed answer is not asked for", func(t *testing.T) {
		// A rebinding name server answers inside first and would answer
		// outside after. The guard asks once and refuses on that answer.
		names := &resolver{answers: map[string][]netip.Addr{"rebind.example": {loopback}}}
		if _, err := get(t, egress(names), "http://rebind.example:"+port); !IsRefusal(err) {
			t.Fatalf("err = %v, want the guard's refusal", err)
		}
		if names.asked != 1 {
			t.Fatalf("the name was asked for %d times, want once", names.asked)
		}
	})
	t.Run("a list that cannot be read refuses the client", func(t *testing.T) {
		e := Egress{Resolve: (&resolver{}).resolve, ConnectTimeout: time.Second,
			AllowedHosts: func(context.Context) ([]string, error) { return nil, errors.New("database down") }}
		if _, err := e.Client(context.Background(), server.URL); err == nil {
			t.Fatal("a client was made without the operator's list")
		}
	})
}

func TestARedirectIsNotFollowed(t *testing.T) {
	var followed bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/elsewhere" {
			followed = true
			return
		}
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer server.Close()
	response, err := get(t, egress(&resolver{}, "127.0.0.1"), server.URL)
	if err != nil || response.StatusCode != http.StatusFound || followed {
		t.Fatalf("response = %v, %v, followed %v", response, err, followed)
	}
}

// rows is Rows with one model for Ada.
type rows struct {
	endpoints map[string]map[int64]Endpoint
}

func (r rows) Menu(_ context.Context, userID string) ([]Option, error) {
	var options []Option
	for id, endpoint := range r.endpoints[userID] {
		options = append(options, Option{ID: Choice(id), RowID: id, ModelID: endpoint.ModelID, Label: endpoint.Label})
	}
	return options, nil
}

func (r rows) Endpoint(_ context.Context, userID string, id int64) (Endpoint, bool, error) {
	endpoint, found := r.endpoints[userID][id]
	return endpoint, found, nil
}

func TestAChosenOwnModelIsCalledAtItsOwnAddressAndStreams(t *testing.T) {
	type seen struct{ path, auth, org, model string }
	var got seen
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		got = seen{r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("X-Org"), body.Model}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, line := range []string{
			`data: {"choices":[{"delta":{"content":"Hello "}}]}`,
			`data: {"choices":[{"delta":{"content":"from your endpoint"}}]}`,
			`data: [DONE]`,
		} {
			fmt.Fprintf(w, "%s\n\n", line)
			w.(http.Flusher).Flush()
		}
	}))
	defer server.Close()

	seal := newSeal(t)
	key, _ := seal.Seal(ada, "fake-ada-key")
	headers, _ := SealedHeaders(seal, ada, map[string]string{"X-Org": "org-1"})
	connections := Connections{
		Rows: rows{endpoints: map[string]map[int64]Endpoint{ada: {
			7: {ModelID: "her-model", Label: "Her model", BaseURL: server.URL + "/v1/", Enabled: true, SealedKey: key, SealedHeaders: SealedHeadersOf(headers)},
			8: {ModelID: "off-model", Label: "Off model", BaseURL: server.URL + "/v1", Enabled: false, SealedKey: key},
		}}},
		Keys:   seal,
		Egress: egress(&resolver{}, "127.0.0.1"),
		Retry:  model.RetryPolicy{Attempts: 1, Backoff: time.Millisecond, MaxBackoff: time.Millisecond},
	}
	if err := connections.Check(); err != nil {
		t.Fatal(err)
	}

	opened, err := connections.Open(context.Background(), ada, 7)
	if err != nil {
		t.Fatal(err)
	}
	var streamed []string
	answer, err := opened.Generate(context.Background(), model.Request{Messages: []model.Message{model.User("hi")}}, func(chunk model.Chunk) {
		if chunk.Kind == model.ChunkText {
			streamed = append(streamed, chunk.Text)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(streamed, "|") != "Hello |from your endpoint" || answer.Message.Content != "Hello from your endpoint" {
		t.Fatalf("streamed %q, answered %q", streamed, answer.Message.Content)
	}
	if got != (seen{"/v1/chat/completions", "Bearer fake-ada-key", "org-1", "her-model"}) {
		t.Fatalf("the endpoint saw %+v", got)
	}

	t.Run("another account's id opens nothing", func(t *testing.T) {
		var unavailable *Unavailable
		if _, err := connections.Open(context.Background(), ben, 7); !errors.As(err, &unavailable) || !strings.Contains(unavailable.Message, "removed") {
			t.Fatalf("Ben opened Ada's model: %v", err)
		}
	})
	t.Run("a switched-off model is told, not called", func(t *testing.T) {
		var unavailable *Unavailable
		if _, err := connections.Open(context.Background(), ada, 8); !errors.As(err, &unavailable) || !strings.Contains(unavailable.Message, "switched off") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a key sealed for another account does not open", func(t *testing.T) {
		stolen := connections
		stolen.Rows = rows{endpoints: map[string]map[int64]Endpoint{ben: {7: {ModelID: "her-model", Label: "Her model", BaseURL: server.URL, Enabled: true, SealedKey: key}}}}
		var unavailable *Unavailable
		if _, err := stolen.Open(context.Background(), ben, 7); !errors.As(err, &unavailable) || !strings.Contains(unavailable.Message, "can no longer be read") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("an endpoint inside the network fails without a retry", func(t *testing.T) {
		inside := connections
		inside.Egress = egress(&resolver{})
		inside.Retry = model.RetryPolicy{Attempts: 3, Backoff: time.Millisecond, MaxBackoff: time.Millisecond}
		opened, err := inside.Open(context.Background(), ada, 7)
		if err != nil {
			t.Fatal(err)
		}
		_, err = opened.Generate(context.Background(), model.Request{Messages: []model.Message{model.User("hi")}}, func(model.Chunk) {})
		var failed *model.Error
		if !errors.As(err, &failed) || failed.Retryable || !strings.Contains(failed.Message, "inside this server's network") {
			t.Fatalf("err = %v, want a refusal that is not retried", err)
		}
	})
}

// opener records what it was asked to open.
type opener struct{ asked []string }

func (o *opener) Open(_ context.Context, userID string, id int64) (model.Model, error) {
	o.asked = append(o.asked, fmt.Sprintf("%s/%d", userID, id))
	return nil, nil
}

func TestTheResolverSendsAProxyNameToTheProxyAndAnOwnChoiceToTheAccountsConnection(t *testing.T) {
	var proxied []string
	own := &opener{}
	resolver := Resolver{Proxy: func(name string) (model.Model, error) { proxied = append(proxied, name); return nil, nil }, Own: own}
	if err := resolver.Check(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gpt-4o", "own:12", "openai/own:12"} {
		if _, err := resolver.Open(context.Background(), ada, name); err != nil {
			t.Fatal(err)
		}
	}
	if fmt.Sprint(proxied) != "[gpt-4o openai/own:12]" || fmt.Sprint(own.asked) != "["+ada+"/12]" {
		t.Fatalf("proxy got %v, own got %v", proxied, own.asked)
	}
}

// Every menu labels an own model the same way, and a proxy name is its
// own label.
func TestMenuEntriesLabelAnAccountsOwnModels(t *testing.T) {
	entries := MenuEntries([]string{"gpt-4o", "own:7"}, []Option{{ID: "own:7", Label: "Her model", Connection: "models.example.org"}})
	raw, _ := json.Marshal(entries)
	want := `[{"id":"gpt-4o","label":"gpt-4o","own":false},{"id":"own:7","label":"Her model","own":true,"connection":"models.example.org"}]`
	if string(raw) != want {
		t.Fatalf("entries\n got %s\nwant %s", raw, want)
	}
}
