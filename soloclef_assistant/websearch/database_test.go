package websearch

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/sealedsecret"
)

// databaseVariable names a throwaway Postgres brought to the schema by
// soloclef_schema's `migrate up`. The test empties the accounts table, so
// it must never name a database anybody uses.
const databaseVariable = "SOLOCLEF_API_TEST_WEBSEARCH_DATABASE"

const (
	ada = "aaaaaaaa-0000-4000-8000-000000000001"
	ben = "bbbbbbbb-0000-4000-8000-000000000002"
)

// A key is read from the account's own connection and opened for it; the
// same ciphertext copied onto another account's row does not open there.
func TestStoredKeysOverARealDatabase(t *testing.T) {
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
	seal, err := sealedsecret.New("fake-deployment-secret", sealedsecret.WebSearchKey)
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := seal.Seal(ada, "fake-exa-key")
	if _, err := pool.Exec(ctx, `TRUNCATE "user" CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO "user" (id, email) VALUES ($1, 'ada@example.org'), ($2, 'ben@example.org')`, ada, ben); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO web_search_connections (user_id, provider, api_key, key_mask)
		VALUES ($1, 'exa', $3, '••••'), ($2, 'exa', $3, '••••')`, ada, ben, sealed); err != nil {
		t.Fatal(err)
	}
	keys := StoredKeys{Pool: pool, Seal: seal}

	if key, found, err := keys.Key(ctx, ada, Exa); err != nil || !found || key != "fake-exa-key" {
		t.Errorf("Ada's Exa key = %q, %v, %v", key, found, err)
	}
	if _, found, err := keys.Key(ctx, ada, Tavily); err != nil || found {
		t.Errorf("Ada has no Tavily key: %v, %v", found, err)
	}
	if key, _, err := keys.Key(ctx, ben, Exa); err == nil || key != "" || strings.Contains(err.Error(), "fake-exa-key") {
		t.Errorf("Ada's ciphertext on Ben's row opened: %q, %v", key, err)
	}
	if connected, err := (Search{Keys: keys}).Connected(ctx, ada, Exa); err != nil || !connected {
		t.Errorf("connected = %v, %v", connected, err)
	}
}
