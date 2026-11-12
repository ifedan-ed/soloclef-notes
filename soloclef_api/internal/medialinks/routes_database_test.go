package medialinks

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_filestore"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres. **Every account in it is
// deleted** (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_MEDIALINKS_DATABASE"

const secret = "a-test-secret-of-no-account"

func TestALinkIsForgedByNobodyAndOutlivesNothing(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	granted := claim{UserID: testpostgres.Alice, ArtifactID: 70, FileID: 700, Expires: now.Add(time.Hour).Unix()}
	token, err := issue(granted, secret)
	if err != nil {
		t.Fatal(err)
	}
	if back, ok := read(token, secret, now); !ok || back != granted {
		t.Fatalf("read %+v %v", back, ok)
	}
	encoded, signature, _ := strings.Cut(token, ".")
	other, _ := issue(claim{UserID: testpostgres.Alice, ArtifactID: 70, FileID: 701, Expires: granted.Expires}, secret)
	otherPayload, _, _ := strings.Cut(other, ".")
	for name, c := range map[string]struct {
		token, secret string
		at            time.Time
	}{
		"past its hour":                    {token, secret, now.Add(time.Hour + time.Second)},
		"signed with another secret":       {token, "another-secret", now},
		"another file under its signature": {otherPayload + "." + signature, secret, now},
		"no signature":                     {encoded, secret, now},
		"an empty signature":               {encoded + ".", secret, now},
		"not base64":                       {"!!." + signature, secret, now},
		"empty":                            {"", secret, now},
	} {
		if back, ok := read(c.token, c.secret, c.at); ok {
			t.Errorf("%s: read as %+v", name, back)
		}
	}
	// A claim that verifies but names nothing is still refused.
	if hollow, _ := issue(claim{UserID: "", ArtifactID: 1, FileID: 1, Expires: granted.Expires}, secret); func() bool { _, ok := read(hollow, secret, now); return ok }() {
		t.Error("a claim with no account was read")
	}
}

func TestMediaLinksAreTheOwnersAloneAndPlayWithRange(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, pool, `
INSERT INTO documents (id, user_id, title, document_type, content, content_hash, status) VALUES
 (500, '`+alice+`', 'Talk', 'ARTIFACT', '', 'a', '{"state": "ready"}'),
 (501, '`+alice+`', 'Going', 'ARTIFACT', '', 'b', '{"state": "deleting"}'),
 (600, '`+bob+`',   'His', 'ARTIFACT', '', 'c', '{"state": "ready"}');
INSERT INTO artifacts (id, document_id, user_id, format) VALUES
 (70, 500, '`+alice+`', 'podcast'), (71, 501, '`+alice+`', 'podcast'), (80, 600, '`+bob+`', 'podcast');
INSERT INTO artifact_files (id, artifact_id, role, storage_backend, storage_key, original_filename, mime_type, size_bytes, checksum_sha256) VALUES
 (700, 70, 'primary', 'local', 'artifacts/70/talk.mp3', 'talk.mp3', 'audio/mpeg', 10, 'c700'),
 (701, 70, 'preview', 'local', 'artifacts/70/cover.png', 'cover.png', 'image/png', 4, 'c701'),
 (710, 71, 'primary', 'local', 'artifacts/71/going.mp4', 'going.mp4', 'video/mp4', 5, 'c710'),
 (800, 80, 'primary', 'local', 'artifacts/80/his.mp3', 'his.mp3', 'audio/mpeg', 6, 'c800');`)

	directory := t.TempDir()
	for key, content := range map[string]string{"artifacts/70/talk.mp3": "0123456789", "artifacts/80/his.mp3": "secret"} {
		path := filepath.Join(directory, filepath.FromSlash(key))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	local, err := filestore.OpenLocal(directory)
	if err != nil {
		t.Fatal(err)
	}
	stores, err := filestore.NewStores(local)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "0"))
	routes := Routes{Pool: pool, Files: stores, Secret: secret, StallTimeout: time.Minute, Errors: errorenvelope.Writer{},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now }}
	if err := routes.Check(); err != nil {
		t.Fatal(err)
	}
	routes.Register(api)
	// The header names the caller; a request without it has none, as a
	// request to the public route has none.
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if caller := request.Header.Get("X-Test-Caller"); caller != "" {
			method := principal.MethodSession
			if request.Header.Get("X-Test-Key") != "" {
				method = principal.MethodPersonalAccessToken
			}
			request = request.WithContext(principal.WithPrincipal(request.Context(), principal.Principal{UserID: caller, Method: method}))
		}
		mux.ServeHTTP(response, request)
	}))
	defer server.Close()
	do := func(method, path, body string, headers ...string) (*http.Response, string) {
		t.Helper()
		request, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i+1 < len(headers); i += 2 {
			request.Header.Set(headers[i], headers[i+1])
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		read, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response, string(read)
	}
	type answer struct {
		Links []struct {
			ArtifactID int64  `json:"artifact_id"`
			FileID     int64  `json:"file_id"`
			URL        string `json:"url"`
			MimeType   string `json:"mime_type"`
			ExpiresAt  string `json:"expires_at"`
		} `json:"links"`
	}
	links := func(userID, body string) answer {
		t.Helper()
		response, text := do("POST", IssuePath, body, "X-Test-Caller", userID)
		if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatalf("%d %s %q", response.StatusCode, text, response.Header.Get("Cache-Control"))
		}
		var got answer
		if err := json.Unmarshal([]byte(text), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	// Her audio is linked. A picture, a file named under the wrong artifact,
	// a file of an artifact being deleted, his file and a file that does not
	// exist are left out, without a word.
	const everything = `{"files": [{"artifact_id": 70, "file_id": 700}, {"artifact_id": 70, "file_id": 701},
	  {"artifact_id": 71, "file_id": 700}, {"artifact_id": 71, "file_id": 710},
	  {"artifact_id": 80, "file_id": 800}, {"artifact_id": 70, "file_id": 424242}]}`
	hers := links(alice, everything)
	if len(hers.Links) != 1 || hers.Links[0].FileID != 700 || hers.Links[0].ArtifactID != 70 || hers.Links[0].MimeType != "audio/mpeg" ||
		hers.Links[0].ExpiresAt != "2026-11-12T21:00:00Z" || !strings.HasPrefix(hers.Links[0].URL, "/api/v1/artifact-media/") {
		t.Fatalf("her links: %+v", hers.Links)
	}
	his := links(bob, everything)
	if len(his.Links) != 1 || his.Links[0].FileID != 800 {
		t.Fatalf("his links: %+v", his.Links)
	}

	for name, body := range map[string]string{
		"no files":       `{"files": []}`,
		"not JSON":       `{`,
		"an id of zero":  `{"files": [{"artifact_id": 0, "file_id": 700}]}`,
		"too many files": `{"files": [` + strings.TrimSuffix(strings.Repeat(`{"artifact_id": 70, "file_id": 700},`, 25), ",") + `]}`,
	} {
		if response, text := do("POST", IssuePath, body, "X-Test-Caller", alice); response.StatusCode != 422 {
			t.Errorf("%s: %d %s", name, response.StatusCode, text)
		}
	}
	// An API key reads files as itself; it cannot mint a bearer link.
	if response, text := do("POST", IssuePath, everything, "X-Test-Caller", alice, "X-Test-Key", "1"); response.StatusCode != 403 {
		t.Errorf("a key minted links: %d %s", response.StatusCode, text)
	}

	// The link plays with no caller at all, whole and by range.
	response, body := do("GET", hers.Links[0].URL, "")
	if response.StatusCode != 200 || body != "0123456789" || response.Header.Get("Content-Type") != "audio/mpeg" ||
		response.Header.Get("Accept-Ranges") != "bytes" || response.Header.Get("Referrer-Policy") != "no-referrer" ||
		response.Header.Get("ETag") != `"c700"` || !strings.HasPrefix(response.Header.Get("Cache-Control"), "private") ||
		!strings.HasPrefix(response.Header.Get("Content-Disposition"), "inline") {
		t.Errorf("the whole file: %d %q %v", response.StatusCode, body, response.Header)
	}
	response, body = do("GET", hers.Links[0].URL, "", "Range", "bytes=2-5")
	if response.StatusCode != 206 || body != "2345" || response.Header.Get("Content-Range") != "bytes 2-5/10" {
		t.Errorf("a range: %d %q %q", response.StatusCode, body, response.Header.Get("Content-Range"))
	}
	response, body = do("GET", hers.Links[0].URL, "", "Range", "bytes=7-")
	if response.StatusCode != 206 || body != "789" {
		t.Errorf("an open range: %d %q", response.StatusCode, body)
	}

	// Every way a link is not good is the same 404.
	refused := func(name, path string) {
		t.Helper()
		if response, body := do("GET", path, ""); response.StatusCode != 404 || !strings.Contains(body, mediaAbsent) || strings.Contains(body, "0123456789") {
			t.Errorf("%s: %d %s", name, response.StatusCode, body)
		}
	}
	token := strings.TrimPrefix(hers.Links[0].URL, mediaPrefix)
	payload, signature, _ := strings.Cut(token, ".")
	refused("a token cut short", mediaPrefix+payload)
	refused("a signature changed", mediaPrefix+payload+"."+signature[:len(signature)-2]+"AA")
	// His signature under her claim: a link is for the file it was made for.
	_, hisSignature, _ := strings.Cut(strings.TrimPrefix(his.Links[0].URL, mediaPrefix), ".")
	refused("her claim under his signature", mediaPrefix+payload+"."+hisSignature)
	forged, _ := issue(claim{UserID: bob, ArtifactID: 70, FileID: 700, Expires: now.Add(time.Hour).Unix()}, secret)
	refused("a well-signed claim naming him and her file", mediaPrefix+forged)
	picture, _ := issue(claim{UserID: alice, ArtifactID: 70, FileID: 701, Expires: now.Add(time.Hour).Unix()}, secret)
	refused("a well-signed claim for a file that is not media", mediaPrefix+picture)

	now = now.Add(linkLifetime + time.Second)
	refused("the link after twelve hours", hers.Links[0].URL)
	now = now.Add(-linkLifetime)

	// A link stops working the moment what it names is gone or its account
	// is closed, though the link itself has hours left.
	testpostgres.Exec(t, pool, `UPDATE "user" SET is_active = false WHERE id = '`+alice+`'`)
	refused("the link of a closed account", hers.Links[0].URL)
	testpostgres.Exec(t, pool, `UPDATE "user" SET is_active = true WHERE id = '`+alice+`';
UPDATE documents SET status = '{"state": "deleting"}' WHERE id = 500`)
	refused("the link of an artifact being deleted", hers.Links[0].URL)
	testpostgres.Exec(t, pool, `UPDATE documents SET status = '{"state": "ready"}' WHERE id = 500; DELETE FROM artifact_files WHERE id = 700`)
	refused("the link of a deleted file", hers.Links[0].URL)
	if response, body := do("GET", his.Links[0].URL, ""); response.StatusCode != 200 || body != "secret" {
		t.Errorf("his own link: %d %q", response.StatusCode, body)
	}
}
