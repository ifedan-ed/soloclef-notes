package documentlifecycle

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/valkey-io/valkey-go"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/celeryqueue"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/documentdeletion"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/indexqueue"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice/parserchoicetest"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/uploads"
	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_celery/queues"
	"git.danvics.com/danvics/soloclef/soloclef_filestore"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

const (
	databaseVariable = "SOLOCLEF_API_TEST_LIFECYCLE_DATABASE"
	redisVariable    = "SOLOCLEF_API_TEST_LIFECYCLE_REDIS"
	qdrantVariable   = "SOLOCLEF_API_TEST_LIFECYCLE_QDRANT"
	// The file store's own test variables, so one MinIO serves both suites.
	s3EndpointVariable = "SOLOCLEF_TEST_S3_ENDPOINT"
	s3AccessVariable   = "SOLOCLEF_TEST_S3_ACCESS_KEY"
	s3SecretVariable   = "SOLOCLEF_TEST_S3_SECRET_KEY"
)

// The two accounts (internal/testpostgres) and a connection of each. Every
// request is the owner's unless a test says it is the stranger's.
const (
	owner       = testpostgres.Alice
	stranger    = testpostgres.Bob
	ownDrive    = int64(800)
	othersDrive = int64(801)
)

// stack is the routes over the real services.
type stack struct {
	t          *testing.T
	pool       *pgxpool.Pool
	redis      valkey.Client
	store      *filestore.S3
	qdrant     documentdeletion.Qdrant
	server     *httptest.Server
	collection string
	// caller is the account the next request is made as.
	caller string
}

func need(t *testing.T, variable string) string {
	t.Helper()
	value := os.Getenv(variable)
	if value == "" {
		t.Skipf("%s is not set; this test needs four throwaway services (see doc.go)", variable)
	}
	return value
}

func random(t *testing.T) string {
	t.Helper()
	var raw [6]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(raw[:])
}

func newStack(t *testing.T) *stack {
	t.Helper()
	_, redisURL, qdrantURL := need(t, databaseVariable), need(t, redisVariable), need(t, qdrantVariable)
	endpoint, access, secret := need(t, s3EndpointVariable), need(t, s3AccessVariable), need(t, s3SecretVariable)
	ctx := context.Background()
	s := &stack{t: t}

	// Postgres, migrated and emptied, with the two accounts and one
	// connection each.
	pool := testpostgres.Open(t, databaseVariable)
	testpostgres.Exec(t, pool, `
INSERT INTO search_source_connectors (id, user_id, name, connector_type, config) VALUES
 (800, '`+owner+`', 'My drive', 'GOOGLE_DRIVE_CONNECTOR', '{}'),
 (801, '`+stranger+`', 'Their drive', 'GOOGLE_DRIVE_CONNECTOR', '{}')`)
	s.pool, s.caller = pool, owner
	var err error

	// Redis, emptied: the test counts what is on the indexing queue.
	option, err := valkey.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	option.DisableCache = true
	s.redis, err = valkey.NewClient(option)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.redis.Close)
	if err := s.redis.Do(ctx, s.redis.B().Flushdb().Build()).Error(); err != nil {
		t.Fatal(err)
	}

	// MinIO: a bucket of this run's own, removed with everything in it.
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := minio.New(parsed.Host, &minio.Options{Creds: credentials.NewStaticV4(access, secret, ""), Secure: parsed.Scheme == "https"})
	if err != nil {
		t.Fatal(err)
	}
	bucket := "lifecycle-" + random(t)
	if err := admin.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: "us-east-1"}); err != nil {
		t.Fatalf("making the bucket: %v", err)
	}
	t.Cleanup(func() {
		for object := range admin.ListObjects(ctx, bucket, minio.ListObjectsOptions{Recursive: true}) {
			_ = admin.RemoveObject(ctx, bucket, object.Key, minio.RemoveObjectOptions{})
		}
		_ = admin.RemoveBucket(ctx, bucket)
	})
	s.store, err = filestore.OpenS3(filestore.S3Settings{
		Endpoint: endpoint, Bucket: bucket, Region: "us-east-1", PathStyle: true, AccessKey: access, SecretKey: secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.store.Close() })
	stores, err := filestore.NewStores(s.store)
	if err != nil {
		t.Fatal(err)
	}

	// Qdrant: a collection of this run's own.
	s.collection = "lifecycle_" + random(t)
	s.qdrant = documentdeletion.Qdrant{URL: qdrantURL, Collection: s.collection, HTTP: &http.Client{Timeout: 30 * time.Second}}
	s.qdrantCall(http.MethodPut, "/collections/"+s.collection, `{"vectors": {"size": 4, "distance": "Cosine"}}`)
	t.Cleanup(func() { s.qdrantCall(http.MethodDelete, "/collections/"+s.collection, "") })

	// The queue, with a default that is not the indexing queue, as deployed.
	queue, err := celeryqueue.New(redisURL, "soloclef")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(queue.Close)

	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	// Mounted exactly as cmd/api mounts them. Local is the default parser;
	// Datalab is configured; the other two are not.
	if err := Register(api, Dependencies{
		Pool: pool, Queue: queue, Files: stores, Vectors: s.qdrant,
		Parsers:     parserchoicetest.Registry(t, parse.Local, parse.Datalab),
		Limits:      parserchoice.Limits{MaxFileBytes: 64 << 20, MaxFiles: 10},
		UploadStall: 30 * time.Second,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}); err != nil {
		t.Fatal(err)
	}

	s.server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		request = request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: s.caller, Method: principal.MethodSession}))
		api.Adapter().ServeHTTP(response, request)
	}))
	t.Cleanup(s.server.Close)
	return s
}

// qdrantCall makes one request to Qdrant and returns the body; anything but
// a 200 fails the test.
func (s *stack) qdrantCall(method, path, body string) []byte {
	s.t.Helper()
	request, err := http.NewRequest(method, s.qdrant.URL+path, strings.NewReader(body))
	if err != nil {
		s.t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := s.qdrant.HTTP.Do(request)
	if err != nil {
		s.t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		s.t.Fatalf("qdrant %s %s: %d %s", method, path, response.StatusCode, raw)
	}
	return raw
}

func (s *stack) points() int {
	s.t.Helper()
	var counted struct {
		Result struct {
			Count int `json:"count"`
		} `json:"result"`
	}
	if err := json.Unmarshal(s.qdrantCall(http.MethodPost, "/collections/"+s.collection+"/points/count", `{"exact": true}`), &counted); err != nil {
		s.t.Fatal(err)
	}
	return counted.Result.Count
}

// call makes a request to a route and returns the status and the body.
func (s *stack) call(method, path, contentType string, body io.Reader) (int, string) {
	s.t.Helper()
	request, err := http.NewRequest(method, s.server.URL+path, body)
	if err != nil {
		s.t.Fatal(err)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		s.t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		s.t.Fatal(err)
	}
	return response.StatusCode, string(raw)
}

// upload posts files (name to content) with the fields first, and returns
// the ids the route answered.
func (s *stack) upload(parser string, files ...[2]string) (status int, documentIDs, duplicateIDs []int64, raw string) {
	s.t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if parser != "" {
		_ = writer.WriteField("parser", parser)
	}
	for _, file := range files {
		part, _ := writer.CreateFormFile("files", file[0])
		_, _ = io.WriteString(part, file[1])
	}
	_ = writer.Close()
	status, raw = s.call(http.MethodPost, uploads.Path, writer.FormDataContentType(), &body)
	var answer struct {
		DocumentIDs          []int64 `json:"document_ids"`
		DuplicateDocumentIDs []int64 `json:"duplicate_document_ids"`
	}
	if status == http.StatusOK {
		if err := json.Unmarshal([]byte(raw), &answer); err != nil {
			s.t.Fatalf("%v: %s", err, raw)
		}
	}
	return status, answer.DocumentIDs, answer.DuplicateDocumentIDs, raw
}

// queued drains the indexing queue and returns the document ids on it, in
// the order they were queued, proving each message is one the worker's
// reader accepts.
func (s *stack) queued() []int64 {
	s.t.Helper()
	ctx := context.Background()
	var ids []int64
	for {
		raw, err := s.redis.Do(ctx, s.redis.B().Rpop().Key(queues.Key(indexqueue.Queue, 0)).Build()).ToString()
		if valkey.IsValkeyNil(err) {
			break
		}
		if err != nil {
			s.t.Fatal(err)
		}
		message, err := celery.Decode([]byte(raw))
		if err != nil {
			s.t.Fatalf("a queued message the worker's reader refuses: %v", err)
		}
		if message.Task != indexqueue.Task || len(message.Args) != 1 {
			s.t.Fatalf("queued %q with %v", message.Task, message.Args)
		}
		ids = append(ids, int64(message.Args[0].(float64)))
	}
	// Nothing may have gone to the default queue, which Python workers read.
	if waiting, err := s.redis.Do(ctx, s.redis.B().Llen().Key("soloclef").Build()).AsInt64(); err != nil || waiting != 0 {
		s.t.Fatalf("%d messages on the default queue, %v", waiting, err)
	}
	return ids
}

type row struct {
	state, reason, parser, title, contentHash string
	files                                     int
}

func (s *stack) document(id int64) *row {
	s.t.Helper()
	var found row
	var reason, parser *string
	err := s.pool.QueryRow(context.Background(), `
		SELECT status ->> 'state', status ->> 'reason', parser, title, content_hash,
		       (SELECT count(*) FROM document_files WHERE document_id = d.id)
		FROM documents AS d WHERE id = $1`, id).Scan(&found.state, &reason, &parser, &found.title, &found.contentHash, &found.files)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return nil
		}
		s.t.Fatal(err)
	}
	if reason != nil {
		found.reason = *reason
	}
	if parser != nil {
		found.parser = *parser
	}
	return &found
}

func (s *stack) setState(id int64, status string) {
	s.t.Helper()
	if _, err := s.pool.Exec(context.Background(), `UPDATE documents SET status = $2::jsonb WHERE id = $1`, id, status); err != nil {
		s.t.Fatal(err)
	}
}

// newestFile is the document's newest original as its row records it.
func (s *stack) newestFile(id int64) (backend, key, checksum string, size int64) {
	s.t.Helper()
	err := s.pool.QueryRow(context.Background(), `
		SELECT storage_backend, storage_key, checksum_sha256, size_bytes
		FROM document_files WHERE document_id = $1 AND kind = 'ORIGINAL' ORDER BY id DESC LIMIT 1`, id).
		Scan(&backend, &key, &checksum, &size)
	if err != nil {
		s.t.Fatal(err)
	}
	return
}

func (s *stack) object(key string) (string, bool) {
	s.t.Helper()
	body, err := s.store.Open(context.Background(), key)
	if errors.Is(err, filestore.ErrNotFound) {
		return "", false
	}
	if err != nil {
		s.t.Fatal(err)
	}
	defer body.Close()
	raw, err := io.ReadAll(body)
	if err != nil {
		s.t.Fatal(err)
	}
	return string(raw), true
}

func digest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func TestADocumentsLifeAgainstRealServices(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	const thesis, corrected = "%PDF-1.7 the thesis, first draft", "%PDF-1.7 the thesis, corrected"

	var thesisID, notesID int64
	t.Run("an upload stores the files, writes the rows and queues the jobs", func(t *testing.T) {
		status, ids, _, raw := s.upload("datalab", [2]string{"thesis.pdf", thesis}, [2]string{"notes.md", "# Notes\n"})
		if status != http.StatusOK || len(ids) != 2 {
			t.Fatalf("answered %d %s", status, raw)
		}
		thesisID, notesID = ids[0], ids[1]

		document := s.document(thesisID)
		if document.state != "pending" || document.parser != "datalab" || document.title != "thesis.pdf" || document.files != 1 {
			t.Errorf("the row is %+v", document)
		}
		if document.contentHash != digest(thesis) {
			t.Errorf("content_hash %q is not the file's SHA-256", document.contentHash)
		}
		backend, key, checksum, size := s.newestFile(thesisID)
		if backend != "s3" || checksum != digest(thesis) || size != int64(len(thesis)) {
			t.Errorf("the file row says %s %s %d", backend, checksum, size)
		}
		if want := fmt.Sprintf("documents/%s/%d/original/", owner, thesisID); !strings.HasPrefix(key, want) {
			t.Errorf("key %q, want it under %s", key, want)
		}
		if stored, found := s.object(key); !found || stored != thesis {
			t.Errorf("the bucket holds %q under the row's key (found: %v)", stored, found)
		}
		if queued := s.queued(); len(queued) != 2 || queued[0] != thesisID || queued[1] != notesID {
			t.Errorf("queued %v, want %d then %d", queued, thesisID, notesID)
		}
	})

	t.Run("the same bytes again are a duplicate and the copy is not kept", func(t *testing.T) {
		s.setState(thesisID, `{"state": "ready"}`)
		status, ids, duplicates, raw := s.upload("", [2]string{"thesis.pdf", thesis})
		if status != http.StatusOK || len(ids) != 0 || len(duplicates) != 1 || duplicates[0] != thesisID {
			t.Fatalf("answered %d %s", status, raw)
		}
		if document := s.document(thesisID); document.state != "ready" || document.files != 1 || document.parser != "datalab" {
			t.Errorf("a duplicate changed the document: %+v", document)
		}
		if queued := s.queued(); len(queued) != 0 {
			t.Errorf("a duplicate queued %v", queued)
		}
	})

	t.Run("different bytes under the same name replace the file and index again", func(t *testing.T) {
		_, firstKey, _, _ := s.newestFile(thesisID)
		status, ids, _, raw := s.upload("local", [2]string{"thesis.pdf", corrected})
		if status != http.StatusOK || len(ids) != 1 || ids[0] != thesisID {
			t.Fatalf("answered %d %s", status, raw)
		}
		document := s.document(thesisID)
		if document.state != "pending" || document.parser != "local" || document.files != 2 || document.contentHash != digest(corrected) {
			t.Errorf("the row is %+v", document)
		}
		_, key, checksum, _ := s.newestFile(thesisID)
		if key == firstKey || checksum != digest(corrected) {
			t.Errorf("the newest original is %s %s", key, checksum)
		}
		if stored, _ := s.object(key); stored != corrected {
			t.Errorf("the bucket holds %q under the new key", stored)
		}
		// The first original is still there: the indexer reads the newest,
		// and a delete removes both.
		if _, found := s.object(firstKey); !found {
			t.Error("the first original was removed by a re-upload")
		}
		if queued := s.queued(); len(queued) != 1 || queued[0] != thesisID {
			t.Errorf("queued %v", queued)
		}
	})

	t.Run("a document recorded under a stale read is refused, not misfiled", func(t *testing.T) {
		// A file staged as if no document of this name existed, when one
		// does and holds different bytes: the key was built for a document
		// id that will never exist.
		s.setState(thesisID, `{"state": "ready"}`)
		_, err := uploads.PostgresRows{Pool: s.pool}.Record(ctx, uploads.Batch{
			UserID: owner, Parser: "local",
			Files: []uploads.Staged{{
				DocumentID: 999999, Existing: false, Name: "thesis.pdf", Identity: uploads.Identity("thesis.pdf", owner),
				Backend: "s3", Key: "documents/" + owner + "/999999/original/stale.pdf", Size: 3, SHA256: digest("new"),
			}},
		})
		var moved *uploads.ErrMovedMeanwhile
		if !errors.As(err, &moved) {
			t.Fatalf("error %v, want ErrMovedMeanwhile", err)
		}
		if document := s.document(thesisID); document.files != 2 || document.state != "ready" {
			t.Errorf("the refused batch changed the document: %+v", document)
		}
	})

	t.Run("the database refuses a parser that is not one of the four", func(t *testing.T) {
		_, err := s.pool.Exec(ctx, `UPDATE documents SET parser = 'cloud' WHERE id = $1`, thesisID)
		if err == nil || !strings.Contains(err.Error(), "ck_documents_parser") {
			t.Errorf("error %v, want the column's CHECK", err)
		}
	})

	t.Run("a re-index hands the document back, on another parser when asked", func(t *testing.T) {
		s.setState(thesisID, `{"state": "failed", "reason": "The parser did not answer."}`)
		status, raw := s.call(http.MethodPost, fmt.Sprintf("/api/v1/documents/%d/reindex?parser=datalab", thesisID), "", nil)
		if status != http.StatusOK {
			t.Fatalf("answered %d %s", status, raw)
		}
		if document := s.document(thesisID); document.state != "pending" || document.reason != "" || document.parser != "datalab" {
			t.Errorf("the row is %+v", document)
		}
		if queued := s.queued(); len(queued) != 1 || queued[0] != thesisID {
			t.Errorf("queued %v", queued)
		}
		// A second request finds it waiting and queues nothing.
		if status, _ := s.call(http.MethodPost, fmt.Sprintf("/api/v1/documents/%d/reindex", thesisID), "", nil); status != http.StatusConflict {
			t.Errorf("a second re-index answered %d", status)
		}
		if queued := s.queued(); len(queued) != 0 {
			t.Errorf("the refused re-index queued %v", queued)
		}
	})

	t.Run("a job that cannot be queued leaves the document failed with a reason", func(t *testing.T) {
		// A broker address nothing listens on.
		dead, err := celeryqueue.New("redis://127.0.0.1:1/0", "soloclef")
		if err != nil {
			t.Fatal(err)
		}
		defer dead.Close()
		indexing := indexqueue.Indexing{Queue: dead, Marker: indexqueue.PostgresMarker{Pool: s.pool},
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
		if err := indexing.Enqueue(ctx, notesID, indexqueueContext()); !errors.Is(err, indexqueue.ErrNotQueued) {
			t.Fatalf("error %v", err)
		}
		if document := s.document(notesID); document.state != "failed" || document.reason != indexqueue.QueueingFailed {
			t.Errorf("the row is %+v", document)
		}
	})

	t.Run("a connection's parser is saved for its owner only", func(t *testing.T) {
		path := fmt.Sprintf("/api/v1/search-source-connectors/%d/parser", ownDrive)
		if status, raw := s.call(http.MethodGet, path, "", nil); status != http.StatusOK || raw != fmt.Sprintf(`{"id":%d,"parser":null}`, ownDrive) {
			t.Errorf("before any choice: %d %s", status, raw)
		}
		if status, raw := s.call(http.MethodPut, path, "application/json", strings.NewReader(`{"parser": "datalab"}`)); status != http.StatusOK || raw != fmt.Sprintf(`{"id":%d,"parser":"datalab"}`, ownDrive) {
			t.Errorf("saving: %d %s", status, raw)
		}
		var saved *string
		if err := s.pool.QueryRow(ctx, `SELECT parser FROM search_source_connectors WHERE id = $1`, ownDrive).Scan(&saved); err != nil || saved == nil || *saved != "datalab" {
			t.Errorf("the column holds %v, %v", saved, err)
		}
		other := fmt.Sprintf("/api/v1/search-source-connectors/%d/parser", othersDrive)
		if status, _ := s.call(http.MethodPut, other, "application/json", strings.NewReader(`{"parser": "datalab"}`)); status != http.StatusNotFound {
			t.Errorf("somebody else's connection answered %d", status)
		}
		if err := s.pool.QueryRow(ctx, `SELECT parser FROM search_source_connectors WHERE id = $1`, othersDrive).Scan(&saved); err != nil || saved != nil {
			t.Errorf("somebody else's connection now holds %v, %v", saved, err)
		}
	})

	// The isolation rule over the real statements and stores: the other
	// account asks to re-index and to delete the owner's document, and to
	// read the owner's connection. Each is the 404 an unused id gets, and
	// the document, its file and the queue are as they were.
	t.Run("another account can neither re-index nor delete the document", func(t *testing.T) {
		s.setState(thesisID, `{"state": "ready"}`)
		before := s.document(thesisID)
		_, key, _, _ := s.newestFile(thesisID)
		s.caller = stranger
		defer func() { s.caller = owner }()
		const unusedID = 987654
		for _, request := range []struct {
			method, path string
			id           int64
		}{
			{http.MethodPost, "/api/v1/documents/%d/reindex", thesisID},
			{http.MethodPost, "/api/v1/documents/%d/reindex?parser=datalab", thesisID},
			{http.MethodDelete, "/api/v1/documents/%d", thesisID},
			{http.MethodGet, "/api/v1/search-source-connectors/%d/parser", ownDrive},
		} {
			target := fmt.Sprintf(request.path, request.id)
			status, raw := s.call(request.method, target, "", nil)
			unusedStatus, unusedRaw := s.call(request.method, fmt.Sprintf(request.path, unusedID), "", nil)
			if status != http.StatusNotFound || unusedStatus != http.StatusNotFound {
				t.Errorf("%s %s answered %d %s; an unused id answers %d", request.method, target, status, raw, unusedStatus)
			}
			// The two sentences differ only in the id each names.
			want := strings.Replace(sentence(unusedRaw), fmt.Sprint(unusedID), fmt.Sprint(request.id), 1)
			if sentence(raw) == "" || sentence(raw) != want {
				t.Errorf("%s %s is told %q and an unused id %q", request.method, target, sentence(raw), sentence(unusedRaw))
			}
		}
		if after := s.document(thesisID); after == nil || *after != *before {
			t.Errorf("the document went from %+v to %+v", before, after)
		}
		if _, found := s.object(key); !found {
			t.Error("the owner's file is gone from the bucket")
		}
		if queued := s.queued(); len(queued) != 0 {
			t.Errorf("another account's requests queued %v", queued)
		}
		// The same file name uploaded by the other account is a document
		// of its own, under its own key.
		status, ids, duplicates, raw := s.upload("", [2]string{"thesis.pdf", thesis})
		if status != http.StatusOK || len(ids) != 1 || len(duplicates) != 0 || ids[0] == thesisID {
			t.Fatalf("the other account's upload of the same name answered %d %s", status, raw)
		}
		if _, theirs, _, _ := s.newestFile(ids[0]); !strings.HasPrefix(theirs, "documents/"+stranger+"/") {
			t.Errorf("the other account's file is stored under %q", theirs)
		}
		s.queued()
		if status, raw := s.call(http.MethodDelete, fmt.Sprintf("/api/v1/documents/%d", ids[0]), "", nil); status != http.StatusOK {
			t.Fatalf("the other account deleting its own document answered %d %s", status, raw)
		}
	})

	t.Run("a delete leaves nothing in Postgres, Qdrant or the bucket", func(t *testing.T) {
		// The thesis as the indexer would have left it: 2,500 chunks, each
		// with a point whose id is the chunk's id; and a neighbour document
		// whose chunks and points must survive.
		s.setState(thesisID, `{"state": "ready"}`)
		s.index(thesisID, 2500)
		s.index(notesID, 40)
		if got := s.points(); got != 2540 {
			t.Fatalf("%d points before the delete", got)
		}
		var keys []string
		rows, err := s.pool.Query(ctx, `SELECT storage_key FROM document_files WHERE document_id = $1`, thesisID)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var key string
			_ = rows.Scan(&key)
			keys = append(keys, key)
		}
		if len(keys) != 2 {
			t.Fatalf("%d stored files", len(keys))
		}

		started := time.Now()
		status, raw := s.call(http.MethodDelete, fmt.Sprintf("/api/v1/documents/%d", thesisID), "", nil)
		took := time.Since(started)
		if status != http.StatusOK || !strings.Contains(raw, "Document deleted successfully") {
			t.Fatalf("answered %d %s", status, raw)
		}
		t.Logf("deleted a document of 2,500 chunks, 2,500 points and 2 stored files in %s", took.Round(time.Millisecond))

		if s.document(thesisID) != nil {
			t.Error("the document row is still there")
		}
		var chunks, files int
		_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM chunks WHERE document_id = $1`, thesisID).Scan(&chunks)
		_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM document_files WHERE document_id = $1`, thesisID).Scan(&files)
		if chunks != 0 || files != 0 {
			t.Errorf("%d chunks and %d file rows are left", chunks, files)
		}
		for _, key := range keys {
			if _, found := s.object(key); found {
				t.Errorf("the bucket still holds %s", key)
			}
		}
		// Only the neighbour's points are left.
		if got := s.points(); got != 40 {
			t.Errorf("%d points after the delete, want the neighbour's 40", got)
		}
		_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM chunks WHERE document_id = $1`, notesID).Scan(&chunks)
		if chunks != 40 {
			t.Errorf("the neighbour has %d chunks", chunks)
		}

		// Asked again, it is simply not there.
		if status, _ := s.call(http.MethodDelete, fmt.Sprintf("/api/v1/documents/%d", thesisID), "", nil); status != http.StatusNotFound {
			t.Errorf("a second delete answered %d", status)
		}
	})

	t.Run("a delete that was cut off is finished by asking again", func(t *testing.T) {
		// The notes document, left as a removal that died after the marker
		// and one batch would leave it: marked, with chunks and files.
		s.setState(notesID, `{"state": "deleting"}`)
		_, key, _, _ := s.newestFile(notesID)
		status, raw := s.call(http.MethodDelete, fmt.Sprintf("/api/v1/documents/%d", notesID), "", nil)
		if status != http.StatusOK {
			t.Fatalf("answered %d %s", status, raw)
		}
		if s.document(notesID) != nil || s.points() != 0 {
			t.Errorf("the document or its %d points are still there", s.points())
		}
		if _, found := s.object(key); found {
			t.Error("its file is still in the bucket")
		}
	})

	t.Run("a 64 MB file is streamed to the bucket, not held", func(t *testing.T) {
		const size = 64 << 20
		reader, writer := io.Pipe()
		form := multipart.NewWriter(writer)
		digested := sha256.New()
		go func() {
			part, err := form.CreateFormFile("files", "large.pdf")
			if err == nil {
				// 64 MB of a repeating kilobyte, digested as it is sent so
				// the test holds none of it either.
				block := bytes.Repeat([]byte("0123456789abcdef"), 64)
				for sent := 0; sent < size && err == nil; sent += len(block) {
					digested.Write(block)
					_, err = part.Write(block)
				}
			}
			if err == nil {
				err = form.Close()
			}
			writer.CloseWithError(err)
		}()

		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		started := time.Now()
		status, raw := s.call(http.MethodPost, uploads.Path, form.FormDataContentType(), reader)
		took := time.Since(started)
		runtime.ReadMemStats(&after)
		if status != http.StatusOK {
			t.Fatalf("answered %d %s", status, raw)
		}
		var answer struct {
			DocumentIDs []int64 `json:"document_ids"`
		}
		if err := json.Unmarshal([]byte(raw), &answer); err != nil || len(answer.DocumentIDs) != 1 {
			t.Fatalf("%v: %s", err, raw)
		}
		_, key, checksum, stored := s.newestFile(answer.DocumentIDs[0])
		if stored != size || checksum != hex.EncodeToString(digested.Sum(nil)) {
			t.Errorf("the row records %d bytes and %s", stored, checksum)
		}
		info, err := s.store.Stat(ctx, key)
		if err != nil || info.Size != size {
			t.Errorf("the bucket holds %d bytes, %v", info.Size, err)
		}
		// Everything allocated during the upload, by both ends of it, live
		// or not: far less than one copy of the file.
		allocated := after.TotalAlloc - before.TotalAlloc
		t.Logf("64 MB streamed to MinIO in %s (%.0f MB/s); %.1f MB allocated in total while it ran",
			took.Round(time.Millisecond), 64/took.Seconds(), float64(allocated)/(1<<20))
		if allocated > size/2 {
			t.Errorf("%d bytes were allocated during a %d byte upload; the file is being held", allocated, size)
		}
		s.queued()
	})

	t.Run("a name freed by a delete can be uploaded again", func(t *testing.T) {
		status, ids, _, raw := s.upload("", [2]string{"thesis.pdf", thesis})
		if status != http.StatusOK || len(ids) != 1 || ids[0] == thesisID {
			t.Fatalf("answered %d %s", status, raw)
		}
		if document := s.document(ids[0]); document.parser != "local" || document.state != "pending" {
			t.Errorf("the row is %+v; with no parser named it must record the default by name", document)
		}
	})
}

// index gives a document `count` chunks and a point for each, the way the
// indexing worker leaves one.
func (s *stack) index(documentID int64, count int) {
	s.t.Helper()
	rows, err := s.pool.Query(context.Background(), `
		INSERT INTO chunks (content, "position", document_id, user_id)
		SELECT 'chunk ' || n, n, d.id, d.user_id
		FROM documents AS d, generate_series(1, $2::int) AS n
		WHERE d.id = $1
		RETURNING id`, documentID, count)
	if err != nil {
		s.t.Fatal(err)
	}
	var points []string
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			s.t.Fatal(err)
		}
		points = append(points, fmt.Sprintf(`{"id": %d, "vector": [0.1, 0.2, 0.3, 0.4], "payload": {"document_id": %d}}`, id, documentID))
	}
	if err := rows.Err(); err != nil {
		s.t.Fatal(err)
	}
	s.qdrantCall(http.MethodPut, "/collections/"+s.collection+"/points?wait=true", `{"points": [`+strings.Join(points, ",")+`]}`)
}

func indexqueueContext() produce.LogContext { return produce.LogContext{RequestID: "lifecycle-test"} }

// sentence is the message of an error envelope, "" for anything else.
func sentence(raw string) string {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(raw), &envelope)
	return envelope.Error.Message
}
