package searchconnectors

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
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/routeanalytics"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres for this package's database
// test. **Every account in it is deleted** (internal/testpostgres), so it
// must never name a database anyone uses. With it unset the test is skipped.
const databaseVariable = "SOLOCLEF_API_TEST_SEARCHCONNECTORS_DATABASE"

// rowRemover removes a document's row, standing in for the document delete's
// own removal (vectors, chunks and stored files are another package's).
type rowRemover struct{ pool *pgxpool.Pool }

func (r rowRemover) Remove(ctx context.Context, documentID int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM documents WHERE id = $1`, documentID)
	return err
}

type noRevocation struct{}

func (noRevocation) Revoke(context.Context, int64, string, []byte) {}

// The isolation test: every route of the package over the real statements,
// with two accounts. Account B can neither see nor change anything of
// account A's, on any method; A can; and no stored credential is ever in an
// answer.
func TestOneAccountNeverSeesOrChangesAnothersConnections(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	ctx := context.Background()
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	// Alice: a Drive with two documents, a server, and two more whose names
	// differ only by case and so test the order. Bob: one server of his own.
	testpostgres.Exec(t, pool, `
INSERT INTO search_source_connectors (id, user_id, name, connector_type, is_indexable, config) VALUES
 (1, '`+alice+`', 'Drive',  'GOOGLE_DRIVE_CONNECTOR', true,
     '{"refresh_token": "fake-alice-grant", "selected_folders": ["Reports"]}'),
 (2, '`+alice+`', 'tools',  'MCP_CONNECTOR', false,
     '{"server_config": {"url": "https://mcp.example.test/mcp", "headers": {"Authorization": "Bearer fake-alice-key-0123456789"}}, "trusted_tools": ["search"]}'),
 (3, '`+alice+`', 'alpha',  'WEBCRAWLER_CONNECTOR', false, '{"INITIAL_URLS": "https://example.test", "FIRECRAWL_API_KEY": "fake-alice-crawl"}'),
 (4, '`+alice+`', 'Alpha',  'NEXTCLOUD_CONNECTOR', false, '{"app_password": "fake-alice-password"}'),
 (5, '`+bob+`',   'His tools', 'MCP_CONNECTOR', false,
     '{"server_config": {"url": "https://his.example.test/mcp"}, "trusted_tools": []}');
INSERT INTO documents (id, user_id, title, document_type, content, content_hash, connector_id) VALUES
 (10, '`+alice+`', 'From the Drive', 'FILE', 'text', 'hash-10', 1),
 (11, '`+alice+`', 'Also from it',   'FILE', 'text', 'hash-11', 1);`)

	store := Store{Pool: pool}
	tools := &bumps{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	envelope := errorenvelope.Writer{ReportURL: "https://notes.example/contact"}
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	ListRoute{Rows: store, Errors: envelope, Logger: logger}.Register(api)
	OneRoute{Rows: store, Errors: envelope, Logger: logger}.Register(api)
	ServersRoute{Rows: store, Errors: envelope, Logger: logger}.Register(api)
	ServerRoute{Rows: store, Errors: envelope, Logger: logger}.Register(api)
	EnabledRoute{Changes: store, Tools: tools, Errors: envelope, Logger: logger}.Register(api)
	ServerDeleteRoute{Changes: store, Tools: tools, Errors: envelope, Logger: logger}.Register(api)
	TrustRoute{Changes: store, Tools: tools, Errors: envelope, Logger: logger}.Register(api)
	TrustRoute{Remove: true, Changes: store, Tools: tools, Errors: envelope, Logger: logger}.Register(api)
	ConnectionDeleteRoute{Connections: store, Removals: store, Documents: rowRemover{pool}, Revocation: noRevocation{},
		Tools: tools, Analytics: routeanalytics.Capture{Sink: &events{}}, Errors: envelope, Logger: logger}.Register(api)

	caller := alice
	withCaller := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		api.Adapter().ServeHTTP(response, request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: caller, Method: principal.MethodSession})))
	})
	bodies := requestbody.Middleware{
		Covers: requestbody.CoveringRoutes("POST "+TrustPath, "POST "+UntrustPath, "PUT "+EnabledPath),
	}
	handler := requestcontext.Middleware(logger, bodies.Wrap(withCaller))
	as := func(userID, method, path, body string) (int, string) {
		t.Helper()
		caller = userID
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		answered := recorder.Body.String()
		// The rule that holds for every answer of every route.
		if strings.Contains(answered, "fake-") {
			t.Fatalf("%s %s answered a stored credential: %s", method, path, answered)
		}
		return recorder.Code, answered
	}
	// The whole of Alice's estate as the database holds it, to compare
	// before and after everything Bob tries.
	state := func() string {
		var text string
		err := pool.QueryRow(ctx, `
SELECT coalesce((SELECT string_agg(id::text || ':' || enabled::text || ':' || config::text, ' | ' ORDER BY id)
                 FROM search_source_connectors WHERE user_id = $1), '')
    || ' # ' ||
       coalesce((SELECT string_agg(id::text || ':' || status::text, ' | ' ORDER BY id)
                 FROM documents WHERE user_id = $1), '')`, alice).Scan(&text)
		if err != nil {
			t.Fatal(err)
		}
		return text
	}

	t.Run("each account lists its own, by name and then id", func(t *testing.T) {
		status, hers := as(alice, http.MethodGet, ListPath, "")
		if status != http.StatusOK {
			t.Fatalf("answered %d %s", status, hers)
		}
		// lower(name) puts "alpha" (3) and "Alpha" (4) together, id breaking
		// the tie; then Drive, then tools.
		last := -1
		for _, id := range []string{`"id":3,`, `"id":4,`, `"id":1,`, `"id":2,`} {
			at := strings.Index(hers, id)
			if at < 0 || at < last {
				t.Fatalf("her list is out of order or missing %s: %s", id, hers)
			}
			last = at
		}
		if strings.Contains(hers, `"id":5,`) || strings.Contains(hers, "his.example.test") {
			t.Errorf("her list holds his server: %s", hers)
		}
		for _, gone := range []string{"workspace_id", "enabled_in_workspace", "is_mine"} {
			if strings.Contains(hers, gone) {
				t.Errorf("the answer still carries %s: %s", gone, hers)
			}
		}
		if !strings.Contains(hers, `"enabled":true`) || !strings.Contains(hers, `"selected_folders":["Reports"]`) {
			t.Errorf("the switch or the public config is missing: %s", hers)
		}

		status, his := as(bob, http.MethodGet, ListPath, "")
		if status != http.StatusOK || !strings.Contains(his, `"id":5,`) {
			t.Fatalf("his list answered %d %s", status, his)
		}
		for _, hersOnly := range []string{`"id":1,`, `"id":2,`, `"id":3,`, `"id":4,`, "mcp.example.test", "Reports"} {
			if strings.Contains(his, hersOnly) {
				t.Errorf("his list holds %s of hers: %s", hersOnly, his)
			}
		}

		// The page is cut by the database: the second and third of hers.
		status, page := as(alice, http.MethodGet, ListPath+"?skip=1&limit=2", "")
		if status != http.StatusOK || !strings.Contains(page, `"id":4,`) || !strings.Contains(page, `"id":1,`) ||
			strings.Contains(page, `"id":3,`) || strings.Contains(page, `"id":2,`) {
			t.Errorf("the page answered %d %s", status, page)
		}
		for _, bad := range []string{"?skip=-1", "?limit=0", "?limit=501", "?limit=abc", "?skip=99999999999999999999"} {
			if status, raw := as(alice, http.MethodGet, ListPath+bad, ""); status != http.StatusUnprocessableEntity {
				t.Errorf("%s answered %d %s", bad, status, raw)
			}
		}

		status, servers := as(alice, http.MethodGet, ServersPath, "")
		if status != http.StatusOK || !strings.Contains(servers, `"id":2,`) || strings.Contains(servers, `"id":5,`) {
			t.Errorf("her servers answered %d %s", status, servers)
		}
		status, servers = as(bob, http.MethodGet, ServersPath, "")
		if status != http.StatusOK || !strings.Contains(servers, `"id":5,`) || strings.Contains(servers, `"id":2,`) {
			t.Errorf("his servers answered %d %s", status, servers)
		}
	})

	t.Run("another account's connection is not found, on every method, and nothing changes", func(t *testing.T) {
		before := state()
		for _, attempt := range []struct{ method, path, body, says string }{
			{http.MethodGet, "/api/v1/search-source-connectors/1", "", connectorAbsent},
			{http.MethodGet, "/api/v1/search-source-connectors/2", "", connectorAbsent},
			{http.MethodGet, "/api/v1/connectors/mcp/2", "", serverAbsent},
			{http.MethodPut, "/api/v1/search-source-connectors/1/enabled", `{"enabled": false}`, connectorAbsent},
			{http.MethodPut, "/api/v1/search-source-connectors/2/enabled", `{"enabled": false}`, connectorAbsent},
			{http.MethodPost, "/api/v1/connectors/mcp/2/trust-tool", `{"tool_name": "summarise"}`, serverAbsent},
			{http.MethodPost, "/api/v1/connectors/mcp/2/untrust-tool", `{"tool_name": "search"}`, serverAbsent},
			{http.MethodDelete, "/api/v1/connectors/mcp/2", "", serverAbsent},
			{http.MethodDelete, "/api/v1/search-source-connectors/1", "", connectorAbsent},
			{http.MethodDelete, "/api/v1/search-source-connectors/2", "", connectorAbsent},
		} {
			status, raw := as(bob, attempt.method, attempt.path, attempt.body)
			if status != http.StatusNotFound || !strings.Contains(raw, attempt.says) {
				t.Errorf("%s %s answered %d %s", attempt.method, attempt.path, status, raw)
			}
			// The same answer as for an id nobody has, so an id cannot be probed.
			nobodys := strings.NewReplacer("/1", "/999", "/2", "/999").Replace(attempt.path)
			if missing, _ := as(bob, attempt.method, nobodys, attempt.body); missing != status {
				t.Errorf("%s %s: %d for hers and %d for an id nobody has", attempt.method, attempt.path, status, missing)
			}
		}
		// The removal's own statements, asked directly as Bob about her
		// connection: they match nothing.
		if err := store.MarkDocumentsDeleting(ctx, bob, 1); err != nil {
			t.Fatal(err)
		}
		if ids, err := store.DocumentIDs(ctx, bob, 1, 100); err != nil || len(ids) != 0 {
			t.Errorf("her documents were listed for him: %v, %v", ids, err)
		}
		if deleted, err := store.DeleteConnection(ctx, bob, 1); err != nil || deleted {
			t.Errorf("he deleted her connection: %v, %v", deleted, err)
		}
		if after := state(); after != before {
			t.Errorf("her rows changed\nbefore %s\n after %s", before, after)
		}
		if len(tools.accounts) != 0 {
			t.Errorf("a refused write moved a counter: %v", tools.accounts)
		}
	})

	t.Run("the owner reads, switches, edits and removes her own", func(t *testing.T) {
		status, one := as(alice, http.MethodGet, "/api/v1/connectors/mcp/2", "")
		if status != http.StatusOK || !strings.Contains(one, `"server_config":{"url":"https://mcp.example.test/mcp"`) ||
			!strings.Contains(one, `"enabled":true`) {
			t.Fatalf("her server answered %d %s", status, one)
		}

		status, switched := as(alice, http.MethodPut, "/api/v1/search-source-connectors/2/enabled", `{"enabled": false}`)
		if status != http.StatusOK || !strings.Contains(switched, `"enabled":false`) {
			t.Fatalf("the switch answered %d %s", status, switched)
		}
		if _, read := as(alice, http.MethodGet, "/api/v1/search-source-connectors/2", ""); !strings.Contains(read, `"enabled":false`) {
			t.Errorf("the read does not carry the switch: %s", read)
		}

		status, trusted := as(alice, http.MethodPost, "/api/v1/connectors/mcp/2/trust-tool", `{"tool_name": "summarise"}`)
		if status != http.StatusOK || !strings.Contains(trusted, `"trusted_tools":["search","summarise"]`) {
			t.Fatalf("the trust answered %d %s", status, trusted)
		}
		// Only the one key was written: the address and the key are as they were.
		var config string
		if err := pool.QueryRow(ctx, `SELECT config::text FROM search_source_connectors WHERE id = 2`).Scan(&config); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(config, "Bearer fake-alice-key-0123456789") || !strings.Contains(config, "https://mcp.example.test/mcp") ||
			!strings.Contains(config, `"trusted_tools": ["search", "summarise"]`) {
			t.Errorf("the config is now %s", config)
		}
		status, untrusted := as(alice, http.MethodPost, "/api/v1/connectors/mcp/2/untrust-tool", `{"tool_name": "search"}`)
		if status != http.StatusOK || !strings.Contains(untrusted, `"trusted_tools":["summarise"]`) {
			t.Errorf("the untrust answered %d %s", status, untrusted)
		}
		// The switch and the two edits each moved her counter, and only hers.
		if len(tools.accounts) != 3 {
			t.Errorf("the counter was moved for %v, want hers three times", tools.accounts)
		}
		for _, account := range tools.accounts {
			if account != alice {
				t.Errorf("a counter was moved for %s", account)
			}
		}

		// Removing the Drive removes its documents and then its row.
		status, removed := as(alice, http.MethodDelete, "/api/v1/search-source-connectors/1", "")
		if status != http.StatusOK || !strings.Contains(removed, `"documents_deleted":2`) {
			t.Fatalf("the removal answered %d %s", status, removed)
		}
		var left int
		if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM documents WHERE user_id = $1) +
			(SELECT count(*) FROM search_source_connectors WHERE id = 1)`, alice).Scan(&left); err != nil || left != 0 {
			t.Errorf("%d rows of the Drive are left, %v", left, err)
		}
		if status, raw := as(alice, http.MethodDelete, "/api/v1/connectors/mcp/2", ""); status != http.StatusNoContent {
			t.Errorf("removing her server answered %d %s", status, raw)
		}
		// His server was never touched by any of it.
		if status, his := as(bob, http.MethodGet, "/api/v1/connectors/mcp/5", ""); status != http.StatusOK || !strings.Contains(his, "his.example.test") {
			t.Errorf("his server answered %d %s", status, his)
		}
	})
}
