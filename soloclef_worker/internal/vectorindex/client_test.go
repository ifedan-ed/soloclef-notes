package vectorindex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// These tests hold the client to the requests Qdrant's REST API documents,
// against a stand-in that records them. That the real server accepts them,
// and that a search then finds the points, is the end-to-end test's to show
// (scripts/index_end_to_end_test.sh).

type call struct {
	method, path, key string
	body              map[string]any
}

// server answers each request with whatever `answer` returns for it.
type server struct {
	*httptest.Server
	calls  []call
	answer func(c call) (status int, result any)
}

// owner is an account's uuid as Postgres prints it.
const owner = "0b1f2f64-3c0e-4f0a-9d52-6a1f0c7e4b11"

func newServer(t *testing.T) *server {
	t.Helper()
	s := &server{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		made := call{method: r.Method, path: r.URL.RequestURI(), key: r.Header.Get("api-key")}
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			if err := json.Unmarshal(raw, &made.body); err != nil {
				t.Errorf("%s %s sent a body that is not JSON: %v", made.method, made.path, err)
			}
		}
		s.calls = append(s.calls, made)
		status, result := http.StatusOK, any(true)
		if s.answer != nil {
			status, result = s.answer(made)
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]any{"result": result, "status": "ok"})
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *server) client(t *testing.T, dimension int) *Client {
	t.Helper()
	client, err := New(Config{
		URL: s.URL + "/", APIKey: "qdrant-key", Alias: "chunks", Collection: "chunks_test_1",
		Dimension: dimension, HTTP: s.Server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestUpsertWritesThePointShapeSearchReadsAndWaits(t *testing.T) {
	server := newServer(t)
	err := server.client(t, 2).Upsert(context.Background(), []Point{
		{ChunkID: 501, Vector: []float32{0.5, -1}, UserID: owner, DocumentType: "FILE", DocumentID: 41},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(server.calls) != 1 {
		t.Fatalf("%d requests", len(server.calls))
	}
	made := server.calls[0]
	// The alias, never the collection; and wait=true, because the caller
	// commits rows on the strength of this answer.
	if made.method != http.MethodPut || made.path != "/collections/chunks/points?wait=true" || made.key != "qdrant-key" {
		t.Errorf("request = %s %s with key %q", made.method, made.path, made.key)
	}
	want := map[string]any{"points": []any{map[string]any{
		"id":     float64(501),
		"vector": []any{0.5, float64(-1)},
		// The owner's uuid as text: Qdrant's tenant index is a keyword
		// index, and the search filters on the same string.
		"payload": map[string]any{"user_id": owner, "document_type": "FILE", "document_id": float64(41)},
	}}}
	if !reflect.DeepEqual(made.body, want) {
		t.Errorf("body = %v\nwant   %v", made.body, want)
	}
}

// A point with no owner belongs to no account's part of the collection.
func TestUpsertRefusesAPointWithNoOwner(t *testing.T) {
	server := newServer(t)
	err := server.client(t, 1).Upsert(context.Background(), []Point{{ChunkID: 9, Vector: []float32{1}}})
	if err == nil || !strings.Contains(err.Error(), "chunk 9 has no owner") {
		t.Fatalf("err = %v", err)
	}
	if len(server.calls) != 0 {
		t.Errorf("%d requests were made for a point that cannot be filed", len(server.calls))
	}
}

func TestUpsertSendsALongDocumentInSeveralRequests(t *testing.T) {
	server := newServer(t)
	points := make([]Point, 2*upsertBatch+3)
	for index := range points {
		points[index] = Point{ChunkID: int64(index + 1), Vector: []float32{1}, UserID: owner}
	}
	if err := server.client(t, 1).Upsert(context.Background(), points); err != nil {
		t.Fatal(err)
	}
	var sizes []int
	for _, made := range server.calls {
		sizes = append(sizes, len(made.body["points"].([]any)))
	}
	if !reflect.DeepEqual(sizes, []int{upsertBatch, upsertBatch, 3}) {
		t.Errorf("requests carried %v points", sizes)
	}
}

func TestAVectorOfTheWrongLengthIsNeverSent(t *testing.T) {
	server := newServer(t)
	err := server.client(t, 4).Upsert(context.Background(), []Point{{ChunkID: 1, Vector: []float32{1, 2}}})
	if err == nil || len(server.calls) != 0 {
		t.Fatalf("err = %v after %d requests", err, len(server.calls))
	}
}

func TestDeleteNamesTheChunksAndNothingIsSentForNone(t *testing.T) {
	server := newServer(t)
	client := server.client(t, 1)
	if err := client.Delete(context.Background(), nil); err != nil || len(server.calls) != 0 {
		t.Fatalf("deleting nothing: err = %v, %d requests", err, len(server.calls))
	}
	if err := client.Delete(context.Background(), []int64{3, 9}); err != nil {
		t.Fatal(err)
	}
	made := server.calls[0]
	if made.method != http.MethodPost || made.path != "/collections/chunks/points/delete?wait=true" {
		t.Errorf("request = %s %s", made.method, made.path)
	}
	if !reflect.DeepEqual(made.body, map[string]any{"points": []any{float64(3), float64(9)}}) {
		t.Errorf("body = %v", made.body)
	}
}

func TestPresentSaysWhichChunksHaveAPoint(t *testing.T) {
	server := newServer(t)
	server.answer = func(call) (int, any) {
		return http.StatusOK, []any{map[string]any{"id": 2}, map[string]any{"id": 5}}
	}
	present, err := server.client(t, 1).Present(context.Background(), []int64{2, 3, 5})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(present, map[int64]bool{2: true, 5: true}) {
		t.Errorf("present = %v", present)
	}
	// Ids only: the vectors are 4 KB each and nothing here reads them.
	if body := server.calls[0].body; body["with_vector"] != false || body["with_payload"] != false {
		t.Errorf("body = %v", body)
	}
}

func TestWhichFailuresMayPass(t *testing.T) {
	for status, retryable := range map[int]bool{
		http.StatusInternalServerError: true, http.StatusServiceUnavailable: true, http.StatusTooManyRequests: true,
		http.StatusBadRequest: false, http.StatusNotFound: false, http.StatusForbidden: false,
	} {
		server := newServer(t)
		server.answer = func(call) (int, any) { return status, nil }
		err := server.client(t, 1).Delete(context.Background(), []int64{1})
		var failure *Failure
		if !errors.As(err, &failure) || failure.Status != status || failure.Retryable != retryable {
			t.Errorf("status %d: err = %v, want retryable %v", status, err, retryable)
		}
	}
	server := newServer(t)
	client := server.client(t, 1)
	server.Close()
	var failure *Failure
	if err := client.Delete(context.Background(), []int64{1}); !errors.As(err, &failure) || !failure.Retryable || failure.Status != 0 {
		t.Errorf("no answer: err = %v", err)
	}
}

// info is a collection as Qdrant describes it.
func info(size int, distance string, indexed ...string) map[string]any {
	schema := map[string]any{}
	for _, field := range indexed {
		schema[field] = map[string]any{"data_type": "keyword"}
	}
	return map[string]any{
		"config":         map[string]any{"params": map[string]any{"vectors": map[string]any{"size": size, "distance": distance}}},
		"payload_schema": schema,
	}
}

func TestEnsureBuildsAMissingCollectionWithItsIndexesAndAlias(t *testing.T) {
	server := newServer(t)
	created := false
	server.answer = func(made call) (int, any) {
		switch {
		case made.method == http.MethodGet && made.path == "/collections/chunks_test_1":
			if !created {
				return http.StatusNotFound, nil
			}
			return http.StatusOK, info(8, "Cosine")
		case made.method == http.MethodPut && made.path == "/collections/chunks_test_1":
			created = true
		case made.path == "/aliases":
			return http.StatusOK, map[string]any{"aliases": []any{}}
		}
		return http.StatusOK, true
	}

	if err := server.client(t, 8).Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}

	var definition, alias map[string]any
	indexes := map[string]any{}
	for _, made := range server.calls {
		switch {
		case made.method == http.MethodPut && made.path == "/collections/chunks_test_1":
			definition = made.body
		case strings.HasPrefix(made.path, "/collections/chunks_test_1/index"):
			indexes[made.body["field_name"].(string)] = made.body["field_schema"]
		case made.path == "/collections/aliases":
			alias = made.body
		}
	}
	vectors := definition["vectors"].(map[string]any)
	graph := vectors["hnsw_config"].(map[string]any)
	// The measured layout: one unnamed cosine vector, no global graph and a
	// graph per account, and the empty sparse vector that cannot be added
	// later.
	if vectors["size"] != float64(8) || vectors["distance"] != "Cosine" || graph["m"] != float64(0) || graph["payload_m"] != float64(16) {
		t.Errorf("vectors = %v", vectors)
	}
	if _, declared := definition["sparse_vectors"].(map[string]any)["content_bm25"]; !declared {
		t.Errorf("the sparse vector is not declared: %v", definition["sparse_vectors"])
	}
	wantIndexes := map[string]any{
		"user_id":       map[string]any{"type": "keyword", "is_tenant": true},
		"document_type": "keyword",
		"document_id":   "integer",
	}
	if !reflect.DeepEqual(indexes, wantIndexes) {
		t.Errorf("payload indexes = %v\nwant             %v", indexes, wantIndexes)
	}
	wantAlias := map[string]any{"actions": []any{map[string]any{
		"create_alias": map[string]any{"collection_name": "chunks_test_1", "alias_name": "chunks"},
	}}}
	if !reflect.DeepEqual(alias, wantAlias) {
		t.Errorf("alias request = %v", alias)
	}
}

func TestEnsureLeavesACollectionThatIsAlreadyRightAlone(t *testing.T) {
	server := newServer(t)
	server.answer = func(made call) (int, any) {
		if made.path == "/aliases" {
			return http.StatusOK, map[string]any{"aliases": []any{
				map[string]any{"alias_name": "chunks", "collection_name": "chunks_test_1"},
			}}
		}
		return http.StatusOK, info(8, "Cosine", "user_id", "document_type", "document_id")
	}
	if err := server.client(t, 8).Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, made := range server.calls {
		if made.method != http.MethodGet {
			t.Errorf("a collection that was already right was sent %s %s", made.method, made.path)
		}
	}
}

func TestEnsureRefusesACollectionBuiltForAnotherModel(t *testing.T) {
	for name, existing := range map[string]map[string]any{
		"another dimension": info(384, "Cosine"),
		"another distance":  info(8, "Dot"),
		"named vectors":     {"config": map[string]any{"params": map[string]any{"vectors": map[string]any{"dense": map[string]any{"size": 8, "distance": "Cosine"}}}}},
	} {
		server := newServer(t)
		server.answer = func(call) (int, any) { return http.StatusOK, existing }
		if err := server.client(t, 8).Ensure(context.Background()); err == nil {
			t.Errorf("%s: the collection was accepted", name)
		}
		for _, made := range server.calls {
			if made.method != http.MethodGet {
				t.Errorf("%s: the collection was written to (%s %s)", name, made.method, made.path)
			}
		}
	}
}

// A collection somebody else filled has no account on any point. Adding the
// account index to it would make every search of it come back empty and
// look fine.
func TestEnsureRefusesAFilledCollectionWithNoAccountIndex(t *testing.T) {
	server := newServer(t)
	server.answer = func(call) (int, any) {
		filled := info(8, "Cosine", "document_type", "document_id")
		filled["points_count"] = 5393
		return http.StatusOK, filled
	}
	err := server.client(t, 8).Ensure(context.Background())
	if err == nil || !strings.Contains(err.Error(), "5393 points") || !strings.Contains(err.Error(), "user_id") {
		t.Fatalf("err = %v", err)
	}
	for _, made := range server.calls {
		if made.method != http.MethodGet {
			t.Errorf("the collection was written to (%s %s)", made.method, made.path)
		}
	}
}

// Moving the alias is the last step of a reindex and somebody's decision.
func TestEnsureDoesNotMoveAnAliasThatPointsElsewhere(t *testing.T) {
	server := newServer(t)
	server.answer = func(made call) (int, any) {
		if made.path == "/aliases" {
			return http.StatusOK, map[string]any{"aliases": []any{
				map[string]any{"alias_name": "chunks", "collection_name": "chunks_v1"},
			}}
		}
		return http.StatusOK, info(8, "Cosine", "user_id", "document_type", "document_id")
	}
	err := server.client(t, 8).Ensure(context.Background())
	if err == nil || !strings.Contains(err.Error(), "chunks_v1") {
		t.Fatalf("err = %v", err)
	}
}

func TestAClientWithAPartMissingIsRefused(t *testing.T) {
	good := Config{URL: "http://qdrant:6333", Alias: "chunks", Collection: "chunks_1", Dimension: 8, HTTP: http.DefaultClient}
	if _, err := New(good); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Config){
		"no address":                      func(c *Config) { c.URL = "" },
		"an address that is not a URL":    func(c *Config) { c.URL = "qdrant:6333" },
		"no alias":                        func(c *Config) { c.Alias = "" },
		"an alias that is the collection": func(c *Config) { c.Alias = c.Collection },
		"no dimension":                    func(c *Config) { c.Dimension = 0 },
		"no client":                       func(c *Config) { c.HTTP = nil },
	} {
		config := good
		change(&config)
		if _, err := New(config); err == nil {
			t.Errorf("%s: the client was built", name)
		}
	}
}
