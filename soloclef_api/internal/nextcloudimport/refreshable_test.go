package nextcloudimport

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// refreshableAs asks the route as one account.
func refreshableAs(t *testing.T, route RefreshableRoute, userID string) string {
	t.Helper()
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	route.Register(api)
	request := httptest.NewRequest(http.MethodGet, RefreshablePath, nil)
	request = request.WithContext(principal.WithPrincipal(request.Context(), principal.Principal{UserID: userID, Method: principal.MethodSession}))
	recorder := httptest.NewRecorder()
	api.Adapter().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("answered %d %s", recorder.Code, recorder.Body)
	}
	return recorder.Body.String()
}

// Which documents can be refreshed is the caller's own documents paired
// with the caller's own connection for the same Nextcloud account. The
// second account has a connection to the very same Nextcloud account and
// documents whose metadata names it; neither side ever crosses.
func TestRefreshableDocumentsAreTheCallersOwnInARealDatabase(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const server = "https://cloud.example"
	testpostgres.Exec(t, pool, `
INSERT INTO search_source_connectors (id, user_id, name, connector_type, config) VALUES
 (1, '`+testpostgres.Alice+`', 'Nextcloud', 'NEXTCLOUD_CONNECTOR', '{"server_url": "`+server+`", "username": "ada"}'),
 (2, '`+testpostgres.Bob+`', 'Nextcloud', 'NEXTCLOUD_CONNECTOR', '{"server_url": "`+server+`", "username": "ada"}'),
 (3, '`+testpostgres.Alice+`', 'Old server', 'NEXTCLOUD_CONNECTOR', '{"server_url": "https://old.example", "username": "ada"}'),
 (4, '`+testpostgres.Alice+`', 'Google Drive', 'GOOGLE_DRIVE_CONNECTOR', '{"server_url": "`+server+`", "username": "ada"}');
INSERT INTO documents (id, user_id, title, document_type, content, content_hash, status, document_metadata) VALUES
 (10, '`+testpostgres.Alice+`', 'mine', 'FILE', '', 'h10', '{"state": "ready"}',
      '{"nextcloud": {"server_url": "`+server+`", "username": "ada", "path": "/a.pdf"}}'),
 (11, '`+testpostgres.Alice+`', 'another Nextcloud account', 'FILE', '', 'h11', '{"state": "ready"}',
      '{"nextcloud": {"server_url": "`+server+`", "username": "grace", "path": "/b.pdf"}}'),
 (12, '`+testpostgres.Alice+`', 'the old server', 'FILE', '', 'h12', '{"state": "ready"}',
      '{"nextcloud": {"server_url": "https://old.example", "username": "ada", "path": "/c.pdf"}}'),
 (13, '`+testpostgres.Alice+`', 'no path', 'FILE', '', 'h13', '{"state": "ready"}',
      '{"nextcloud": {"server_url": "`+server+`", "username": "ada"}}'),
 (14, '`+testpostgres.Alice+`', 'an upload', 'FILE', '', 'h14', '{"state": "ready"}', '{"FILE_NAME": "d.pdf"}'),
 (15, '`+testpostgres.Alice+`', 'being deleted', 'FILE', '', 'h15', '{"state": "deleting"}',
      '{"nextcloud": {"server_url": "`+server+`", "username": "ada", "path": "/e.pdf"}}'),
 (16, '`+testpostgres.Alice+`', 'failed, and still refreshable', 'FILE', '', 'h16', '{"state": "failed", "reason": "x"}',
      '{"nextcloud": {"server_url": "`+server+`", "username": "ada", "path": "/f.pdf"}}'),
 (20, '`+testpostgres.Bob+`', 'Bob''s', 'FILE', '', 'h20', '{"state": "ready"}',
      '{"nextcloud": {"server_url": "`+server+`", "username": "ada", "path": "/a.pdf"}}')`)

	route := RefreshableRoute{Rows: RefreshableStore{Pool: pool}, ServerURL: server,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if got, want := refreshableAs(t, route, testpostgres.Alice),
		`{"documents":[{"document_id":10,"connector_id":1},{"document_id":16,"connector_id":1}]}`; got != want {
		t.Errorf("Alice is answered\n %s\nwant\n %s", got, want)
	}
	// Bob's document goes with Bob's connection, though Alice's has the
	// lower id and names the same Nextcloud account.
	if got, want := refreshableAs(t, route, testpostgres.Bob), `{"documents":[{"document_id":20,"connector_id":2}]}`; got != want {
		t.Errorf("Bob is answered\n %s\nwant\n %s", got, want)
	}
	// Bob without a connection of his own can refresh nothing, whatever
	// Alice has.
	testpostgres.Exec(t, pool, `DELETE FROM search_source_connectors WHERE id = 2`)
	if got := refreshableAs(t, route, testpostgres.Bob); got != `{"documents":[]}` {
		t.Errorf("Bob with no connection is answered %s", got)
	}
	// A deployment with no Nextcloud: nothing can be refreshed, and the
	// database is not asked.
	none := RefreshableRoute{ServerURL: "", Logger: route.Logger}
	if got := refreshableAs(t, none, testpostgres.Alice); got != `{"documents":[]}` {
		t.Errorf("a deployment with no Nextcloud answered %s", got)
	}
}
