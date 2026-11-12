package documentcontent

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres. **Every account in it is
// deleted** (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_DOCUMENTCONTENT_DATABASE"

func TestADocumentsTextIsReadByItsOwnerAlone(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, pool, `
INSERT INTO documents (id, user_id, title, document_type, content, content_hash, source_markdown, status) VALUES
 (1, '`+alice+`', 'Tide tables', 'FILE', '# Tides', 'h1', NULL, '{"state":"ready"}'),
 (2, '`+alice+`', 'Her note', 'NOTE', 'stale', 'h2', 'edited text', '{"state":"ready"}'),
 (3, '`+alice+`', 'Arriving', 'FILE', '', 'h3', NULL, '{"state":"processing"}'),
 (4, '`+alice+`', 'Broken', 'FILE', '', 'h4', NULL, '{"state":"failed","reason":"the PDF is encrypted"}'),
 (5, '`+alice+`', 'Blank note', 'NOTE', '', 'h5', NULL, '{"state":"ready"}'),
 (6, '`+bob+`',   'His file', 'FILE', '# His', 'h6', NULL, '{"state":"ready"}');
INSERT INTO chunks (document_id, user_id, content, position) VALUES
 (1, '`+alice+`', '# Tides', 0), (1, '`+alice+`', 'more', 1), (6, '`+bob+`', '# His', 0);`)

	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	Route{Database: pool, Errors: errorenvelope.Writer{},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}.Register(api)
	as := func(userID, id string) (int, string) {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/documents/"+id+"/editor-content", nil)
		request = request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: userID, Method: principal.MethodSession}))
		recorder := httptest.NewRecorder()
		api.Adapter().ServeHTTP(recorder, request)
		return recorder.Code, recorder.Body.String()
	}

	for _, c := range []struct {
		name, caller, id string
		status           int
		says             []string
	}{
		{"her file, from its Markdown", alice, "1", 200, []string{`"source_markdown":"# Tides"`, `"chunk_count":2`, `"viewer_mode":"plate"`, `"document_type":"FILE"`}},
		{"her edited note wins over the stored content", alice, "2", 200, []string{`"source_markdown":"edited text"`}},
		{"a blank note is a blank page", alice, "5", 200, []string{`"source_markdown":""`}},
		{"a document being read is a 409 the viewer waits on", alice, "3", 409, []string{"still being processed"}},
		{"a failed document says why", alice, "4", 422, []string{"the PDF is encrypted"}},
		{"his document is not found for her", alice, "6", 404, []string{"Document not found"}},
		{"her document is not found for him", bob, "1", 404, []string{"Document not found"}},
		{"his own reads", bob, "6", 200, []string{`"source_markdown":"# His"`, `"chunk_count":1`}},
		{"an id that is not a number", alice, "x", 422, []string{"document_id"}},
	} {
		status, body := as(c.caller, c.id)
		if status != c.status {
			t.Errorf("%s: %d %s", c.name, status, body)
			continue
		}
		for _, piece := range c.says {
			if !strings.Contains(body, piece) {
				t.Errorf("%s: %s lacks %s", c.name, body, piece)
			}
		}
	}
}

func TestALargeDocumentOpensInTheCodeViewer(t *testing.T) {
	if viewerMode(strings.Repeat("line\n", richEditorMaxLines)) != "monaco" {
		t.Error("five thousand and one lines opened in the rich editor")
	}
	if viewerMode(strings.Repeat("x", richEditorMaxBytes+1)) != "monaco" {
		t.Error("over a megabyte opened in the rich editor")
	}
	if viewerMode("# Short") != "plate" {
		t.Error("a short document opened in the code viewer")
	}
}
