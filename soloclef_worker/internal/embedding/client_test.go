package embedding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// proxy is a stand-in for the model proxy's /embeddings route. It answers a
// vector per text whose first number is the text's own number ("text 17"
// gives 17), so a test can tell that each vector came back in its text's
// place.
type proxy struct {
	*httptest.Server
	dimension int
	// refuse, when set, may answer a request itself; it returns true if it
	// did.
	refuse func(w http.ResponseWriter, texts []string) bool

	mu       sync.Mutex
	batches  []int
	inFlight atomic.Int32
	peak     atomic.Int32
	model    string
	key      string
}

func newProxy(t *testing.T, dimension int) *proxy {
	t.Helper()
	p := &proxy{dimension: dimension}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := p.inFlight.Add(1)
		defer p.inFlight.Add(-1)
		for peak := p.peak.Load(); now > peak && !p.peak.CompareAndSwap(peak, now); peak = p.peak.Load() {
		}
		if r.URL.Path != "/v1/embeddings" || r.Method != http.MethodPost {
			http.Error(w, "not the embeddings route", http.StatusNotFound)
			return
		}
		var request struct {
			Model          string   `json:"model"`
			Input          []string `json:"input"`
			EncodingFormat string   `json:"encoding_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.EncodingFormat != "float" {
			http.Error(w, "bad request body", http.StatusUnprocessableEntity)
			return
		}
		p.mu.Lock()
		p.batches = append(p.batches, len(request.Input))
		p.model, p.key = request.Model, r.Header.Get("Authorization")
		p.mu.Unlock()
		if p.refuse != nil && p.refuse(w, request.Input) {
			return
		}
		type item struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}
		// Answered in reverse, because the shape numbers its vectors and
		// does not promise their order.
		data := make([]item, 0, len(request.Input))
		for index := len(request.Input) - 1; index >= 0; index-- {
			vector := make([]float32, p.dimension)
			fmt.Sscanf(request.Input[index], "text %f", &vector[0])
			data = append(data, item{Index: index, Embedding: vector})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(p.Close)
	return p
}

func testProfile(dimension, batch, concurrency int) Profile {
	return Profile{
		Name: "test", Model: "test-model", Dimension: dimension, Sizer: CharacterSizer,
		ChunkSize: 100, MaxInput: 1000, BatchSize: batch, Concurrency: concurrency,
	}
}

func (p *proxy) client(t *testing.T, profile Profile) *Client {
	t.Helper()
	client, err := New(profile, Config{BaseURL: p.URL + "/v1/", Key: "sk-test", HTTP: p.Server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func numbered(count int) []string {
	texts := make([]string, count)
	for index := range texts {
		texts[index] = fmt.Sprintf("text %d", index)
	}
	return texts
}

func TestEveryTextGetsItsOwnVectorAcrossBatches(t *testing.T) {
	proxy := newProxy(t, 4)
	client := proxy.client(t, testProfile(4, 8, 3))

	vectors, err := client.EmbedDocuments(context.Background(), numbered(27))
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 27 {
		t.Fatalf("%d vectors for 27 texts", len(vectors))
	}
	for index, vector := range vectors {
		if len(vector) != 4 || vector[0] != float32(index) {
			t.Fatalf("text %d got vector %v", index, vector)
		}
	}
	// 27 texts at 8 a request is 8, 8, 8 and 3.
	total := 0
	for _, size := range proxy.batches {
		if size > 8 {
			t.Errorf("a request carried %d texts, over the batch size of 8", size)
		}
		total += size
	}
	if len(proxy.batches) != 4 || total != 27 {
		t.Errorf("requests carried %v texts", proxy.batches)
	}
	if peak := proxy.peak.Load(); peak > 3 {
		t.Errorf("%d requests were in flight at once, over the concurrency of 3", peak)
	}
	if proxy.model != "test-model" || proxy.key != "Bearer sk-test" {
		t.Errorf("the proxy was asked for model %q with %q", proxy.model, proxy.key)
	}
}

func TestNoTextsIsNoRequest(t *testing.T) {
	proxy := newProxy(t, 4)
	vectors, err := proxy.client(t, testProfile(4, 8, 2)).EmbedDocuments(context.Background(), nil)
	if err != nil || len(vectors) != 0 || len(proxy.batches) != 0 {
		t.Fatalf("vectors = %v, err = %v, requests = %v", vectors, err, proxy.batches)
	}
}

// The host refuses a batch while it is busy and takes the same texts in
// smaller batches, so a 400 is halved against the same host.
func TestABatchTheHostRefusesIsHalvedDownToWhatItTakes(t *testing.T) {
	proxy := newProxy(t, 2)
	proxy.refuse = func(w http.ResponseWriter, texts []string) bool {
		if len(texts) > 2 {
			http.Error(w, `{"error":"batch too large"}`, http.StatusBadRequest)
			return true
		}
		return false
	}
	vectors, err := proxy.client(t, testProfile(2, 8, 1)).EmbedDocuments(context.Background(), numbered(8))
	if err != nil {
		t.Fatal(err)
	}
	for index, vector := range vectors {
		if vector[0] != float32(index) {
			t.Fatalf("text %d got vector %v after the halving", index, vector)
		}
	}
	// 8 refused, 4 and 4 refused, then four pairs.
	if got := fmt.Sprint(proxy.batches); got != "[8 4 2 2 4 2 2]" {
		t.Errorf("requests carried %s texts", got)
	}
}

func TestASingleTextTheHostRefusesFailsAndIsNotRetryable(t *testing.T) {
	proxy := newProxy(t, 2)
	proxy.refuse = func(w http.ResponseWriter, texts []string) bool {
		for _, text := range texts {
			if text == "text 2" {
				http.Error(w, "this text cannot be embedded", http.StatusBadRequest)
				return true
			}
		}
		return false
	}
	_, err := proxy.client(t, testProfile(2, 4, 1)).EmbedDocuments(context.Background(), numbered(4))
	var failure *Failure
	if !errors.As(err, &failure) || failure.Status != http.StatusBadRequest || failure.Retryable {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(failure.Detail, "cannot be embedded") {
		t.Errorf("the proxy's own words were lost: %q", failure.Detail)
	}
}

func TestWhichFailuresMayPass(t *testing.T) {
	for _, test := range []struct {
		status    int
		retryable bool
	}{
		{http.StatusInternalServerError, true},
		{http.StatusBadGateway, true},
		{http.StatusServiceUnavailable, true},
		{http.StatusTooManyRequests, true},
		{http.StatusRequestTimeout, true},
		{http.StatusUnauthorized, false},
		{http.StatusForbidden, false},
		{http.StatusNotFound, false},
	} {
		proxy := newProxy(t, 2)
		proxy.refuse = func(w http.ResponseWriter, _ []string) bool {
			http.Error(w, "no", test.status)
			return true
		}
		_, err := proxy.client(t, testProfile(2, 4, 2)).EmbedDocuments(context.Background(), numbered(3))
		var failure *Failure
		if !errors.As(err, &failure) || failure.Status != test.status || failure.Retryable != test.retryable {
			t.Errorf("status %d: err = %v, want retryable %v", test.status, err, test.retryable)
		}
	}
}

func TestAProxyThatDoesNotAnswerMayPass(t *testing.T) {
	proxy := newProxy(t, 2)
	client := proxy.client(t, testProfile(2, 4, 2))
	proxy.Close()
	_, err := client.EmbedDocuments(context.Background(), numbered(3))
	var failure *Failure
	if !errors.As(err, &failure) || failure.Status != 0 || !failure.Retryable {
		t.Fatalf("err = %v", err)
	}
}

// A proxy name repointed at another model answers vectors of another length.
// Writing them would put two models' vectors in one collection.
func TestAVectorOfTheWrongLengthIsRefused(t *testing.T) {
	proxy := newProxy(t, 3)
	err := proxy.client(t, testProfile(1024, 4, 1)).Probe(context.Background())
	var failure *Failure
	if !errors.As(err, &failure) || failure.Retryable {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(failure.Detail, "3 numbers") || !strings.Contains(failure.Detail, "1024") {
		t.Errorf("the message does not give both lengths: %q", failure.Detail)
	}
}

func TestACancelledJobIsNotAFailureOfTheService(t *testing.T) {
	proxy := newProxy(t, 2)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := proxy.client(t, testProfile(2, 4, 2)).EmbedDocuments(ctx, numbered(9))
	var failure *Failure
	if !errors.Is(err, context.Canceled) || errors.As(err, &failure) {
		t.Fatalf("err = %v, want the cancellation itself", err)
	}
}

func TestOneFailedBatchFailsTheWholeDocument(t *testing.T) {
	proxy := newProxy(t, 2)
	proxy.refuse = func(w http.ResponseWriter, texts []string) bool {
		if texts[0] == "text 4" {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return true
		}
		return false
	}
	vectors, err := proxy.client(t, testProfile(2, 2, 2)).EmbedDocuments(context.Background(), numbered(10))
	if err == nil || vectors != nil {
		t.Fatalf("vectors = %v, err = %v: a document must be embedded whole or not at all", vectors, err)
	}
}

func TestTheProfileIsNamedOrTheProcessIsToldWhichExist(t *testing.T) {
	profile, err := ProfileNamed(" bge-m3 ")
	if err != nil || profile.Dimension != 1024 || profile.ChunkSize != 512 {
		t.Fatalf("profile = %+v, err = %v", profile, err)
	}
	for _, name := range []string{"", "bge-large"} {
		_, err := ProfileNamed(name)
		if err == nil || !strings.Contains(err.Error(), "bge-m3") {
			t.Errorf("%q: err = %v, want the list of profiles", name, err)
		}
	}
}

func TestAProfileThatCouldNotWorkIsRefused(t *testing.T) {
	good := testProfile(8, 4, 2)
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Profile){
		"no model":                     func(p *Profile) { p.Model = "" },
		"no dimension":                 func(p *Profile) { p.Dimension = 0 },
		"an unknown sizer":             func(p *Profile) { p.Sizer = "words" },
		"tokens with no tokenizer":     func(p *Profile) { p.Sizer = TokenizerSizer },
		"no batch size":                func(p *Profile) { p.BatchSize = 0 },
		"no concurrency":               func(p *Profile) { p.Concurrency = 0 },
		"a table chunk the model cuts": func(p *Profile) { p.MaxInput = p.ChunkSize*OversizeFactor - 1 },
	} {
		profile := good
		change(&profile)
		if err := profile.Validate(); err == nil {
			t.Errorf("%s: the profile was accepted", name)
		}
	}
	// Every row of the real table has to pass, or the process cannot start.
	for _, profile := range profiles {
		if err := profile.Validate(); err != nil {
			t.Errorf("the table's own profile %s: %v", profile.Name, err)
		}
	}
}

func TestAClientWithAPartMissingIsRefused(t *testing.T) {
	profile := testProfile(8, 4, 2)
	for name, config := range map[string]Config{
		"no address": {Key: "k", HTTP: http.DefaultClient},
		"no key":     {BaseURL: "http://proxy/v1", HTTP: http.DefaultClient},
		"no client":  {BaseURL: "http://proxy/v1", Key: "k"},
	} {
		if _, err := New(profile, config); err == nil {
			t.Errorf("%s: the client was built", name)
		}
	}
}
