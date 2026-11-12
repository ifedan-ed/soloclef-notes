package ownmodels

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The statements over a real Postgres brought to the schema by
// soloclef_schema's `migrate up`. Skipped unless the variable names a
// throwaway database: the test empties the accounts and site settings.
//
//	SOLOCLEF_API_TEST_OWNMODELS_DATABASE=postgres://check:check@127.0.0.1:39827/check \
//	  go test ./ownmodels/ -run OverAReal -count=1 -v
const databaseVariable = "SOLOCLEF_API_TEST_OWNMODELS_DATABASE"

// Ada has an OpenAI-compatible connection with a model on, one off, one
// that draws, and a second connection switched off; she also has a
// connection to another provider. Ben has one model of his own. Nothing
// asked as one may answer the other's rows.
const seed = `
TRUNCATE "user", site_settings CASCADE;
INSERT INTO "user" (id, email) VALUES
  ('` + ada + `', 'ada@example.org'),
  ('` + ben + `', 'ben@example.org');
INSERT INTO connections (id, provider, base_url, api_key, extra, scope, enabled, user_id) VALUES
  (1, 'openai_compatible', 'https://models.example.org/v1', 'v1:fake-sealed', '{"headers_sealed":"v1:fake-headers","header_names":["X-Org"]}', 'user', true, '` + ada + `'),
  (2, 'openai_compatible', 'https://off.example.org/v1', 'v1:fake-sealed', '{}', 'user', false, '` + ada + `'),
  (3, 'openai', NULL, 'v1:fake-sealed', '{}', 'user', true, '` + ada + `'),
  (4, 'openai_compatible', 'https://his.example.org/v1', 'v1:fake-his', '{}', 'user', true, '` + ben + `');
INSERT INTO models (id, connection_id, model_id, display_name, supports_chat, supports_image_generation, enabled) VALUES
  (10, 1, 'her-chat', 'Her chat', NULL, NULL, true),
  (11, 1, 'her-off', NULL, NULL, NULL, false),
  (12, 1, 'her-painter', NULL, false, true, true),
  (20, 2, 'on-an-off-connection', NULL, NULL, NULL, true),
  (30, 3, 'gpt-proxied', NULL, NULL, NULL, true),
  (40, 4, 'his-chat', NULL, NULL, NULL, true);
`

func TestTheStatementsOverARealDatabase(t *testing.T) {
	address := os.Getenv(databaseVariable)
	if address == "" {
		t.Skipf("%s is not set; this test needs a throwaway Postgres", databaseVariable)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, seed); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	store := Store{Pool: pool}

	t.Run("the menu is the account's switched-on models of switched-on OpenAI-compatible connections", func(t *testing.T) {
		menu, err := store.Menu(ctx, ada)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, option := range menu {
			got = append(got, fmt.Sprintf("%s %s %s chat=%v images=%v", option.ID, option.Label, option.Connection, option.Chat, option.Images))
		}
		want := "[own:10 Her chat models.example.org chat=true images=false own:12 her-painter models.example.org chat=false images=true]"
		if fmt.Sprint(got) != want {
			t.Fatalf("Ada's menu\n got %v\nwant %s", got, want)
		}
		his, _ := store.Menu(ctx, ben)
		if len(his) != 1 || his[0].ID != "own:40" {
			t.Fatalf("Ben's menu = %+v", his)
		}
	})
	t.Run("an endpoint is its owner's, and says when it is switched off", func(t *testing.T) {
		endpoint, found, err := store.Endpoint(ctx, ada, 10)
		if err != nil || !found || !endpoint.Enabled || endpoint.BaseURL != "https://models.example.org/v1" ||
			endpoint.SealedKey != "v1:fake-sealed" || endpoint.SealedHeaders != "v1:fake-headers" || endpoint.Label != "Her chat" {
			t.Fatalf("Ada's model 10 = %+v, %v, %v", endpoint, found, err)
		}
		if off, found, _ := store.Endpoint(ctx, ada, 20); !found || off.Enabled {
			t.Fatalf("a model of a switched-off connection = %+v, %v", off, found)
		}
		for _, asked := range []struct {
			who string
			id  int64
		}{{ben, 10}, {ada, 40}, {ada, 30}, {ada, 999}} {
			if _, found, err := store.Endpoint(ctx, asked.who, asked.id); found || err != nil {
				t.Errorf("%s asking for model %d found it (%v)", asked.who, asked.id, err)
			}
		}
	})
	t.Run("the operator's allow-list", func(t *testing.T) {
		if hosts, err := store.AllowedHosts(ctx); err != nil || len(hosts) != 0 {
			t.Fatalf("no row = %v, %v", hosts, err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO site_settings (key, value) VALUES ('model_connection_allowed_hosts', '["ollama.lan"]')`); err != nil {
			t.Fatal(err)
		}
		if hosts, err := store.AllowedHosts(ctx); err != nil || fmt.Sprint(hosts) != "[ollama.lan]" {
			t.Fatalf("the list = %v, %v", hosts, err)
		}
		if _, err := pool.Exec(ctx, `UPDATE site_settings SET value = '"ollama.lan"' WHERE key = 'model_connection_allowed_hosts'`); err != nil {
			t.Fatal(err)
		}
		if _, err := store.AllowedHosts(ctx); err == nil {
			t.Fatal("a list that is not a list was read")
		}
	})
}
