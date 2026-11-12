package connectorparser

import (
	"context"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres this test may empty
// (internal/testpostgres deletes every account in it). Unset, the test is
// skipped.
//
//	SOLOCLEF_API_TEST_CONNECTORPARSER_DATABASE=postgres://postgres:check@127.0.0.1:<port>/postgres \
//	    go test ./internal/connectorparser/ -run RealDatabase -count=1 -v
const databaseVariable = "SOLOCLEF_API_TEST_CONNECTORPARSER_DATABASE"

// The two statements against Postgres, with two accounts: a connection's
// parser is read and set by its owner, and for anybody else the connection
// is not there at all, exactly as an id no connection has.
func TestAConnectionsParserOverARealDatabase(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	ctx := context.Background()
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	rows := PostgresRows{Pool: pool}

	var connection int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO search_source_connectors (user_id, name, connector_type, config, parser)
		VALUES ($1::uuid, 'Cloud', 'NEXTCLOUD_CONNECTOR', '{}', 'datalab') RETURNING id`, alice).Scan(&connection); err != nil {
		t.Fatal(err)
	}
	stored := func() string {
		t.Helper()
		var parser string
		if err := pool.QueryRow(ctx, `SELECT parser FROM search_source_connectors WHERE id = $1`, connection).Scan(&parser); err != nil {
			t.Fatal(err)
		}
		return parser
	}

	if choice, err := rows.ParserOf(ctx, connection, alice); err != nil || choice == nil || choice.ConnectorID != connection || choice.Parser != "datalab" {
		t.Fatalf("the owner reads %+v, %v", choice, err)
	}

	// Bob: no read, no write, and the same nil an unused id gives.
	if choice, err := rows.ParserOf(ctx, connection, bob); err != nil || choice != nil {
		t.Errorf("another account read the connection's parser: %+v, %v", choice, err)
	}
	if choice, err := rows.ParserOf(ctx, 1<<40, bob); err != nil || choice != nil {
		t.Errorf("an id no connection has: %+v, %v", choice, err)
	}
	if choice, err := rows.SetParser(ctx, connection, bob, "local"); err != nil || choice != nil {
		t.Errorf("another account set the connection's parser: %+v, %v", choice, err)
	}
	if got := stored(); got != "datalab" {
		t.Errorf("the parser is %q after another account's write; it must still be datalab", got)
	}

	// The owner's write lands, and answers what was stored.
	if choice, err := rows.SetParser(ctx, connection, alice, "local"); err != nil || choice == nil || choice.Parser != "local" || stored() != "local" {
		t.Errorf("the owner set %+v, %v; stored %q", choice, err, stored())
	}
}
