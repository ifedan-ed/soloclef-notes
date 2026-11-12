package publicchat

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres for this package's database
// test. **Every account in it is deleted** (internal/testpostgres), so it
// must never name a database anyone uses. With it unset the test is skipped.
const databaseVariable = "SOLOCLEF_API_TEST_PUBLICCHAT_DATABASE"

// TestTheRoutesOverARealDatabase runs the five statements where the other
// tests of this package use a stand-in: over Postgres, against the schema
// the migrations create.
//
// These routes have no caller, so there is no "account B" to refuse. What
// stands in its place is the token, and that is what this test holds to:
// a link serves what its own snapshot shares and nothing else -- not another
// link's artifact, not an artifact the snapshot does not list, and nothing
// at all once it has expired or been revoked.
func TestTheRoutesOverARealDatabase(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	ctx := context.Background()
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	// Alice has shared a chat that shows one film (901). Her other film
	// (902) and Bob's (903) exist and are in no link of hers. One link has
	// lapsed. An id past thirty-two bits is an ordinary id here: every id
	// column is bigint.
	testpostgres.Exec(t, pool, `
INSERT INTO new_chat_threads (id, user_id, title) VALUES
 (300, '`+alice+`', 'A chat of hers'), (301, '`+alice+`', 'A lapsed chat'), (302, '`+bob+`', 'A chat of his');
INSERT INTO documents (id, user_id, title, document_type, content, content_hash) VALUES
 (901, '`+alice+`', 'A shared film', 'ARTIFACT', 'film', 'h-901'),
 (902, '`+alice+`', 'A film kept private', 'ARTIFACT', 'film', 'h-902'),
 (903, '`+bob+`', 'His film', 'ARTIFACT', 'film', 'h-903'),
 (5000000000, '`+alice+`', 'A film with a wide id', 'ARTIFACT', 'film', 'h-wide');
INSERT INTO artifacts (id, document_id, user_id, format, metadata) VALUES
 (901, 901, '`+alice+`', 'video', '{"slides": [{"slide_number": 1, "title": "One"}], "scene_codes": ["a scene"]}'),
 (902, 902, '`+alice+`', 'video', '{"slides": [], "scene_codes": []}'),
 (903, 903, '`+bob+`',   'video', '{"slides": [], "scene_codes": []}'),
 (5000000000, 5000000000, '`+alice+`', 'video', '{"slides": [], "scene_codes": []}');
INSERT INTO public_chat_snapshots (id, user_id, thread_id, share_token, content_hash,
                                   snapshot_data, message_ids, expires_at) VALUES
 (900, '`+alice+`', 300, 'tok-open', 'h-900',
  '{"title": "A shared chat", "snapshot_at": "2026-03-04T05:06:07+00:00", "messages": [],
    "artifact_ids": [901, 5000000000],
    "podcasts": [{"original_id": 3, "title": "An episode", "transcript": []}]}', '{}', NULL),
 (901, '`+alice+`', 301, 'tok-lapsed', 'h-901',
  '{"title": "A lapsed chat", "snapshot_at": "2026-03-04T05:06:07+00:00", "messages": [], "artifact_ids": [902]}',
  '{}', '2020-01-01 00:00:00+00'),
 (902, '`+bob+`', 302, 'tok-his', 'h-902',
  '{"title": "His chat", "snapshot_at": "2026-03-04T05:06:07+00:00", "messages": [], "artifact_ids": [903]}', '{}', NULL);`)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	envelope := errorenvelope.Writer{ReportURL: "https://notes.example/contact"}
	rows := Store{Pool: pool}
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "0.0.1"))
	PageRoute{Rows: rows, Errors: envelope, Logger: logger}.Register(api)
	VideoRoute{Rows: rows, Errors: envelope, Logger: logger}.Register(api)
	PodcastRoute{Rows: rows, Errors: envelope, Logger: logger}.Register(api)
	server := httptest.NewServer(requestcontext.Middleware(logger, mux))
	defer server.Close()
	get := func(t *testing.T, path string) (int, string) {
		t.Helper()
		// No credential of any kind: that is who these routes answer.
		response, err := server.Client().Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, string(body)
	}

	for name, c := range map[string]struct {
		path   string
		status int
		// holds must be in the body, and never must not.
		holds, never string
	}{
		"a link's page": {"/api/v1/public/tok-open", 200, "A shared chat", "A lapsed chat"},
		"a lapsed link's page says it has lapsed": {"/api/v1/public/tok-lapsed", 410, pageExpired, "A lapsed chat"},
		"a token nobody was given":                {"/api/v1/public/tok-none", 404, pageAbsent, "chat"},

		"a film the link shares":           {"/api/v1/public/tok-open/artifacts/901/video", 200, "A shared film", "private"},
		"a shared film with a wide id":     {"/api/v1/public/tok-open/artifacts/5000000000/video", 200, "A film with a wide id", "private"},
		"her film the link does not share": {"/api/v1/public/tok-open/artifacts/902/video", 404, videoAbsent, "private"},
		"another account's film":           {"/api/v1/public/tok-open/artifacts/903/video", 404, videoAbsent, "His film"},
		"a film nobody has":                {"/api/v1/public/tok-open/artifacts/999/video", 404, videoAbsent, "film\""},
		"a lapsed link shares nothing":     {"/api/v1/public/tok-lapsed/artifacts/902/video", 404, videoAbsent, "private"},
		"his link shares his film":         {"/api/v1/public/tok-his/artifacts/903/video", 200, "His film", "shared film"},
		"and not hers":                     {"/api/v1/public/tok-his/artifacts/901/video", 404, videoAbsent, "A shared film"},

		"an episode the link holds":     {"/api/v1/public/tok-open/podcasts/3", 200, "An episode", "storage_key"},
		"an episode it does not":        {"/api/v1/public/tok-open/podcasts/4", 404, podcastAbsent, "An episode"},
		"an episode through a bad link": {"/api/v1/public/tok-none/podcasts/3", 404, podcastAbsent, "An episode"},
	} {
		t.Run(name, func(t *testing.T) {
			status, body := get(t, c.path)
			if status != c.status {
				t.Fatalf("status %d, want %d: %s", status, c.status, body)
			}
			if !strings.Contains(body, c.holds) {
				t.Errorf("the answer does not hold %q: %s", c.holds, body)
			}
			if strings.Contains(body, c.never) {
				t.Errorf("the answer holds %q: %s", c.never, body)
			}
		})
	}

	// A revoked link is a deleted row, and stops serving at once.
	t.Run("a revoked link serves nothing", func(t *testing.T) {
		if _, err := pool.Exec(ctx, "DELETE FROM public_chat_snapshots WHERE share_token = 'tok-his'"); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"/api/v1/public/tok-his", "/api/v1/public/tok-his/artifacts/903/video"} {
			if status, body := get(t, path); status != http.StatusNotFound {
				t.Errorf("%s: status %d after the revoke: %s", path, status, body)
			}
		}
	})

	t.Run("the statements behind a shared output", func(t *testing.T) {
		testpostgres.Exec(t, pool, `
INSERT INTO artifact_files (id, artifact_id, role, storage_backend, storage_key, original_filename,
                            mime_type, size_bytes, checksum_sha256) VALUES
 (9001, 901, 'preview', 'local', 'artifacts/901/preview.png', 'film.png', 'image/png', 10, 'c-9001'),
 (9002, 901, 'primary', 'local', 'artifacts/901/primary.mp4', 'film.mp4', 'video/mp4', 20, 'c-9002');
UPDATE artifacts SET generation = 4 WHERE id = 901;`)
		if generation, found, err := rows.ArtifactGeneration(ctx, 901); err != nil || !found || generation != 4 {
			t.Errorf("generation %d, found %v, %v", generation, found, err)
		}
		if _, found, err := rows.ArtifactGeneration(ctx, 999); err != nil || found {
			t.Errorf("an artifact nobody has was found: %v, %v", found, err)
		}
		files, err := rows.ArtifactFiles(ctx, 901)
		if err != nil || len(files) != 2 {
			t.Fatalf("%d files, %v", len(files), err)
		}
		formats, err := rows.ArtifactFormats(ctx, []int64{901, 5000000000, 999})
		if err != nil || len(formats) != 2 || formats[901] != "video" || formats[5000000000] != "video" {
			t.Errorf("formats %v, %v", formats, err)
		}
	})
}
