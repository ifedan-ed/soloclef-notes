package endtoend

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
	"unicode"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
	"git.danvics.com/danvics/soloclef/soloclef_filestore"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parserconfig"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/selection"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/vision"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/chunking"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/embedding"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/indexing"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/indexstore"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/vectorindex"
)

// What names the services. All of the first seven are needed, and the
// script that starts the containers sets them.
const (
	envDatabase     = "SOLOCLEF_INDEX_TEST_DATABASE_URL"
	envQdrant       = "SOLOCLEF_INDEX_TEST_QDRANT_URL"
	envS3Endpoint   = "SOLOCLEF_INDEX_TEST_S3_ENDPOINT"
	envS3AccessKey  = "SOLOCLEF_INDEX_TEST_S3_ACCESS_KEY"
	envS3SecretKey  = "SOLOCLEF_INDEX_TEST_S3_SECRET_KEY"
	envRedis        = "SOLOCLEF_INDEX_TEST_REDIS_URL"
	envParseService = "SOLOCLEF_INDEX_TEST_PARSE_SERVICE_URL"
	envTokenizers   = "SOLOCLEF_INDEX_TEST_TOKENIZER_DIR"

	// The real model proxy, for the tests and benchmarks that say they use
	// it. Without both, embeddings come from the stub below.
	envProxyURL     = "LITELLM_URL"
	envProxyKeyFile = "LITELLM_KEY_FILE"

	s3Region = "us-east-1"
)

// stack is the indexing job assembled from its real parts, as cmd/indexer
// assembles it, against the throwaway services.
type stack struct {
	pool      *pgxpool.Pool
	documents *indexstore.Store
	files     *filestore.Stores
	parsers   *selection.Registry
	chunker   *chunking.Chunker
	embedder  *embedding.Client
	vectors   *vectorindex.Client
	indexer   *indexing.Indexer
	profile   embedding.Profile

	qdrantURL string
	alias     string
	redisURL  string

	// userID is the account the run's documents belong to.
	userID string
}

// needs skips the test unless every named service is given.
func needs(tb testing.TB, names ...string) map[string]string {
	tb.Helper()
	values := map[string]string{}
	for _, name := range names {
		value := os.Getenv(name)
		if value == "" {
			tb.Skipf("%s is not set; run scripts/index_end_to_end_test.sh, which starts the services this test needs", name)
		}
		values[name] = value
	}
	return values
}

// realProxy is the proxy's address and key when the run names them.
func realProxy(tb testing.TB) (address, key string, named bool) {
	tb.Helper()
	address, file := os.Getenv(envProxyURL), os.Getenv(envProxyKeyFile)
	if address == "" || file == "" {
		return "", "", false
	}
	content, err := os.ReadFile(file)
	if err != nil {
		tb.Fatalf("%s: %v", envProxyKeyFile, err)
	}
	return address, strings.TrimSpace(string(content)), true
}

// open assembles the job. With useRealProxy the embeddings come from the
// model proxy; otherwise from the stub.
func open(tb testing.TB, useRealProxy bool) *stack {
	tb.Helper()
	env := needs(tb, envDatabase, envQdrant, envS3Endpoint, envS3AccessKey, envS3SecretKey, envRedis, envParseService, envTokenizers)
	ctx := context.Background()
	suffix := randomHex(tb, 4)

	pool, err := pgxpool.New(ctx, env[envDatabase])
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(pool.Close)
	documents := indexstore.New(pool)

	// A bucket of this run's own, made the way an operator makes one.
	bucket := "soloclef-index-" + suffix
	settings := filestore.S3Settings{
		Endpoint: env[envS3Endpoint], Bucket: bucket, Region: s3Region, PathStyle: true,
		AccessKey: env[envS3AccessKey], SecretKey: env[envS3SecretKey],
	}
	admin, err := minio.New(strings.TrimPrefix(env[envS3Endpoint], "http://"), &minio.Options{
		Creds: credentials.NewStaticV4(settings.AccessKey, settings.SecretKey, ""), Region: s3Region,
	})
	if err != nil {
		tb.Fatal(err)
	}
	if err := admin.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: s3Region}); err != nil {
		tb.Fatal(err)
	}
	files, err := filestore.OpenStores(filestore.Settings{Backend: filestore.S3Backend, S3: settings})
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { files.Close() })

	// The parsers, from the same settings a deployment gives: the local
	// parser service is configured and the hosted ones are not.
	parsers, err := parserconfig.Load(func(name string) string {
		return map[string]string{
			parserconfig.DefaultParser:       "local",
			parserconfig.MaxTextBytes:        "10485760",
			parserconfig.MaxMarkdownBytes:    "52428800",
			parserconfig.LocalURL:            env[envParseService],
			parserconfig.LocalTimeoutSeconds: "120",
		}[name]
	})
	if err != nil {
		tb.Fatal(err)
	}

	profile, err := embedding.ProfileNamed("bge-m3")
	if err != nil {
		tb.Fatal(err)
	}
	chunker, err := chunking.Open(profile, env[envTokenizers])
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { chunker.Close() })

	proxyURL, proxyKey := stubProxy(tb, profile.Dimension), "sk-stub"
	if useRealProxy {
		address, key, named := realProxy(tb)
		if !named {
			tb.Skipf("set %s and %s to run this against the real model proxy", envProxyURL, envProxyKeyFile)
		}
		proxyURL, proxyKey = address, key
	}
	embedder, err := embedding.New(profile, embedding.Config{
		BaseURL: proxyURL, Key: proxyKey, HTTP: &http.Client{Timeout: 2 * time.Minute},
	})
	if err != nil {
		tb.Fatal(err)
	}
	if err := embedder.Probe(ctx); err != nil {
		tb.Fatal(err)
	}

	// A collection and an alias of this run's own.
	collection, alias := "chunks_e2e_"+suffix+"_1", "chunks_e2e_"+suffix
	vectors, err := vectorindex.New(vectorindex.Config{
		URL: env[envQdrant], Alias: alias, Collection: collection,
		Dimension: profile.Dimension, HTTP: &http.Client{Timeout: 30 * time.Second},
	})
	if err != nil {
		tb.Fatal(err)
	}
	if err := vectors.Ensure(ctx); err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		request, _ := http.NewRequest(http.MethodDelete, env[envQdrant]+"/collections/"+collection, nil)
		if response, err := http.DefaultClient.Do(request); err == nil {
			response.Body.Close()
		}
	})

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if testing.Verbose() && os.Getenv("SOLOCLEF_INDEX_TEST_LOG") != "" {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	indexer, err := indexing.New(documents, files, parsers, chunker, embedder, vectors,
		// The vision models over the same proxy, as cmd/indexer builds
		// them; no own connections, and no model: these documents do not
		// ask for vision.
		indexing.Config{MaxAttempts: 3, RetryDelay: 10 * time.Millisecond,
			Vision: indexing.ResolvedVision{Resolver: ownmodels.VisionResolver{
				Proxy: vision.Client{BaseURL: proxyURL, Key: proxyKey, HTTP: &http.Client{}, Timeout: time.Minute},
				Own:   ownmodels.NoConnections{},
			}}}, log)
	if err != nil {
		tb.Fatal(err)
	}

	s := &stack{
		pool: pool, documents: documents, files: files, parsers: parsers, chunker: chunker,
		embedder: embedder, vectors: vectors, indexer: indexer, profile: profile,
		qdrantURL: env[envQdrant], alias: alias, redisURL: env[envRedis],
	}
	s.userID = s.newAccount(tb)
	return s
}

// newAccount makes an account, and returns its id as Postgres prints it.
func (s *stack) newAccount(tb testing.TB) (userID string) {
	tb.Helper()
	err := s.pool.QueryRow(context.Background(), `
		INSERT INTO "user" (email, hashed_password, is_verified)
		VALUES ($1, 'not a password', true)
		RETURNING id::text`, "index-"+randomHex(tb, 6)+"@example.test").Scan(&userID)
	if err != nil {
		tb.Fatal(err)
	}
	return userID
}

// upload does what the upload route will: stores the file in the configured
// store, and inserts the pending document and the row that says where its
// file is. parser is the upload's choice, or empty for the default.
func (s *stack) upload(tb testing.TB, userID string, name string, content []byte, parser string) int64 {
	tb.Helper()
	ctx := context.Background()
	var documentID int64
	var chosen *string
	if parser != "" {
		chosen = &parser
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO documents (title, document_type, content, content_hash, user_id, status, parser)
		VALUES ($1, 'FILE', '', $2, $3::uuid, '{"state": "pending"}', $4)
		RETURNING id`, name, randomHex(tb, 32), userID, chosen).Scan(&documentID)
	if err != nil {
		tb.Fatal(err)
	}
	s.attach(tb, userID, documentID, name, content)
	return documentID
}

// attach stores a file and records it as the document's (newest) original.
func (s *stack) attach(tb testing.TB, userID string, documentID int64, name string, content []byte) string {
	tb.Helper()
	ctx := context.Background()
	writer, err := s.files.Writer()
	if err != nil {
		tb.Fatal(err)
	}
	key := fmt.Sprintf("documents/%s/%d/original/%s-%s", userID, documentID, randomHex(tb, 8), name)
	stored, err := writer.PutStream(ctx, key, bytes.NewReader(content), "application/octet-stream")
	if err != nil {
		tb.Fatal(err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO document_files (document_id, user_id, kind, storage_backend, storage_key,
		                            original_filename, mime_type, size_bytes, checksum_sha256)
		VALUES ($1, $2::uuid, 'ORIGINAL', $3, $4, $5, 'application/octet-stream', $6, $7)`,
		documentID, userID, writer.Name(), key, name, stored.Size, stored.SHA256)
	if err != nil {
		tb.Fatal(err)
	}
	return key
}

// status is the document's status as a client reads it.
func (s *stack) status(tb testing.TB, documentID int64) map[string]any {
	tb.Helper()
	var raw []byte
	if err := s.pool.QueryRow(context.Background(), `SELECT status FROM documents WHERE id = $1`, documentID).Scan(&raw); err != nil {
		tb.Fatal(err)
	}
	var status map[string]any
	if err := json.Unmarshal(raw, &status); err != nil {
		tb.Fatal(err)
	}
	return status
}

// chunk is a chunk row as a citation reads it.
type chunk struct {
	ID                 int64
	Content            string
	Position           int
	StartLine, EndLine int
	PageStart, PageEnd *int
}

// chunks is the document's rows in order.
func (s *stack) chunks(tb testing.TB, documentID int64) []chunk {
	tb.Helper()
	rows, err := s.pool.Query(context.Background(), `
		SELECT id, content, "position", start_line, end_line, page_start, page_end
		FROM chunks WHERE document_id = $1 ORDER BY "position"`, documentID)
	if err != nil {
		tb.Fatal(err)
	}
	defer rows.Close()
	var found []chunk
	for rows.Next() {
		var row chunk
		if err := rows.Scan(&row.ID, &row.Content, &row.Position, &row.StartLine, &row.EndLine, &row.PageStart, &row.PageEnd); err != nil {
			tb.Fatal(err)
		}
		found = append(found, row)
	}
	if err := rows.Err(); err != nil {
		tb.Fatal(err)
	}
	return found
}

// hit is one search result: a chunk that came back from Qdrant and was then
// re-read from Postgres inside the account.
type hit struct {
	chunk
	DocumentID int64
	Payload    map[string]any
}

// search is the meaning leg of the product's search, as its reader issues it
// (soloclef_assistant/documents/qdrant.go): the query is embedded,
// Qdrant is asked for the nearest points whose user_id payload is the
// account's uuid, and the ids are re-read from Postgres with the owner
// applied again. The error is returned and not fatal, so a
// reader running beside an index can use it from its own goroutine.
func (s *stack) search(userID string, query string, limit int) ([]hit, error) {
	ctx := context.Background()
	vectors, err := s.embedder.EmbedDocuments(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]any{
		"query": vectors[0],
		"filter": map[string]any{"must": []any{
			map[string]any{"key": vectorindex.TenantField, "match": map[string]any{"value": userID}},
		}},
		"limit": limit, "with_payload": true,
		"params": map[string]any{"hnsw_ef": 200},
	})
	response, err := http.Post(s.qdrantURL+"/collections/"+s.alias+"/points/query", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(response.Body)
		return nil, fmt.Errorf("the search answered %d: %s", response.StatusCode, detail)
	}
	var answer struct {
		Result struct {
			Points []struct {
				ID      int64          `json:"id"`
				Payload map[string]any `json:"payload"`
			} `json:"points"`
		} `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&answer); err != nil {
		return nil, err
	}

	var hits []hit
	for _, point := range answer.Result.Points {
		found := hit{Payload: point.Payload}
		err := s.pool.QueryRow(ctx, `
			SELECT id, content, "position", start_line, end_line, page_start, page_end, document_id
			FROM chunks WHERE id = $1 AND user_id = $2::uuid`, point.ID, userID).
			Scan(&found.ID, &found.Content, &found.Position, &found.StartLine, &found.EndLine, &found.PageStart, &found.PageEnd, &found.DocumentID)
		if err != nil {
			// A point whose row is gone: not a result.
			continue
		}
		hits = append(hits, found)
	}
	return hits, nil
}

// pointCount is how many points Qdrant holds for a document.
func (s *stack) pointCount(tb testing.TB, documentID int64) int {
	tb.Helper()
	body, _ := json.Marshal(map[string]any{
		"filter": map[string]any{"must": []any{map[string]any{"key": "document_id", "match": map[string]any{"value": documentID}}}},
		"exact":  true,
	})
	response, err := http.Post(s.qdrantURL+"/collections/"+s.alias+"/points/count", "application/json", bytes.NewReader(body))
	if err != nil {
		tb.Fatal(err)
	}
	defer response.Body.Close()
	var answer struct {
		Result struct {
			Count int `json:"count"`
		} `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&answer); err != nil {
		tb.Fatal(err)
	}
	return answer.Result.Count
}

// index runs the job and requires that the document ended ready.
func (s *stack) index(tb testing.TB, documentID int64) indexing.Result {
	tb.Helper()
	result, err := s.indexer.Index(context.Background(), documentID)
	if err != nil || result.Outcome != indexing.Indexed {
		tb.Fatalf("document %d: outcome = %s (%s), err = %v", documentID, result.Outcome, result.Reason, err)
	}
	return result
}

func randomHex(tb testing.TB, size int) string {
	tb.Helper()
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		tb.Fatal(err)
	}
	return hex.EncodeToString(raw)
}

// stubProxy serves the OpenAI embeddings shape with vectors made from the
// text's own words: each word adds to one of the vector's numbers, chosen by
// hashing the word, and the vector is scaled to length one. Two texts that
// share words are therefore near each other by cosine, which is enough for a
// search to be asserted on, and the same text always gets the same vector.
// It is not a model. What it lets the tests prove is the plumbing.
func stubProxy(tb testing.TB, dimension int) string {
	tb.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Input []string `json:"input"`
		}
		if r.URL.Path != "/v1/embeddings" || json.NewDecoder(r.Body).Decode(&request) != nil {
			http.Error(w, "not an embeddings request", http.StatusBadRequest)
			return
		}
		type item struct {
			Object    string    `json:"object"`
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}
		data := make([]item, len(request.Input))
		for index, text := range request.Input {
			data[index] = item{Object: "embedding", Index: index, Embedding: wordVector(text, dimension)}
		}
		json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data, "model": "stub"})
	}))
	tb.Cleanup(server.Close)
	return server.URL + "/v1"
}

func wordVector(text string, dimension int) []float32 {
	vector := make([]float32, dimension)
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	for _, word := range words {
		hash := fnv.New32a()
		hash.Write([]byte(word))
		vector[hash.Sum32()%uint32(dimension)]++
	}
	var length float64
	for _, value := range vector {
		length += float64(value) * float64(value)
	}
	if length == 0 {
		// Cosine is undefined for a zero vector; a text with no words gets a
		// fixed direction of its own.
		vector[0] = 1
		return vector
	}
	scale := float32(1 / math.Sqrt(length))
	for index := range vector {
		vector[index] *= scale
	}
	return vector
}

// pdf is a small real PDF with a text layer: one page per entry, each line
// of an entry on a line of its own. Built in code so that no document is
// checked in. The text must be plain ASCII without parentheses or
// backslashes; this is a fixture, not a PDF library.
func pdf(pages ...[]string) []byte {
	var out bytes.Buffer
	var offsets []int
	object := func(body string) {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", len(offsets), body)
	}
	out.WriteString("%PDF-1.4\n")
	object("<< /Type /Catalog /Pages 2 0 R >>")
	kids := ""
	for index := range pages {
		kids += fmt.Sprintf("%d 0 R ", 4+2*index)
	}
	object(fmt.Sprintf("<< /Type /Pages /Kids [ %s] /Count %d >>", kids, len(pages)))
	object("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	for index, lines := range pages {
		object(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "+
			"/Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", 5+2*index))
		// 14-point leading, starting an inch from the top.
		stream := "BT /F1 11 Tf 14 TL 72 720 Td"
		for _, line := range lines {
			stream += fmt.Sprintf(" (%s) Tj T*", line)
		}
		stream += " ET"
		object(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return out.Bytes()
}
