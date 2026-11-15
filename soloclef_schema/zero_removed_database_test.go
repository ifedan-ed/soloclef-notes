package schema_test

import (
	"context"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	schema "git.danvics.com/danvics/soloclef/soloclef_schema"
)

// zeroRemoved is 00029, which took Rocicorp Zero's publication and every
// object zero-cache made for itself out of the database.
const zeroRemoved = 29

// After every migration nothing of Zero is left: no publication, and the
// schema is not one a logical replication client could follow.
func TestAMigratedDatabaseHasNothingOfZero(t *testing.T) {
	pool := migrated(t)
	if left := zeroObjects(t, pool); len(left) != 0 {
		t.Fatalf("the migrated schema still has %v", left)
	}
}

// What zero-cache 1.6 makes when it starts on a database (its schemas with
// their functions, its DDL event triggers, its own publication, and, where
// the server allows logical replication, its slot) is all removed by the
// Up. Rolled back, the publication is back as 00001 made it, still sending
// no secret and nothing without its owner.
func TestZerosLeftoversAreRemovedAndTheRollbackRestoresThePublication(t *testing.T) {
	pool := migrated(t)
	ctx := context.Background()
	before := versionBefore(t, zeroRemoved)
	if _, err := schema.DownTo(ctx, pool, before, testKeys); err != nil {
		t.Fatalf("rolling back to %d: %v", before, err)
	}
	publicationKeepsSecretsAndOwners(t, pool)

	// zero-cache's own objects, by the names it gives them.
	exec(t, pool, `CREATE SCHEMA zero`)
	exec(t, pool, `CREATE TABLE zero.permissions (permissions jsonb)`)
	exec(t, pool, `CREATE SCHEMA zero_0`)
	exec(t, pool, `CREATE SCHEMA "zero_0/cdc"`)
	exec(t, pool, `CREATE SCHEMA "zero_0/cvr"`)
	exec(t, pool, `CREATE TABLE "zero_0/cvr".clients (id text)`)
	exec(t, pool, `CREATE FUNCTION zero_0.emit_ddl_start() RETURNS event_trigger LANGUAGE plpgsql AS $$ BEGIN END $$`)
	exec(t, pool, `CREATE EVENT TRIGGER zero_ddl_start_0 ON ddl_command_start EXECUTE FUNCTION zero_0.emit_ddl_start()`)
	exec(t, pool, `CREATE EVENT TRIGGER zero_ddl_end_0 ON ddl_command_end EXECUTE FUNCTION zero_0.emit_ddl_start()`)
	exec(t, pool, `CREATE PUBLICATION _zero_metadata_0 FOR TABLE zero.permissions`)
	slot := column[string](t, pool, `SHOW wal_level`)[0] == "logical"
	if slot {
		exec(t, pool, `SELECT pg_create_logical_replication_slot('zero_0_a', 'pgoutput')`)
	} else {
		t.Log("the test server is not at wal_level=logical, so no slot is made and its removal is not tried")
	}
	// Someone else's schema and publication with a name like Zero's stay.
	exec(t, pool, `CREATE SCHEMA zeroes`)

	if _, err := schema.UpTo(ctx, pool, zeroRemoved, testKeys); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	if left := zeroObjects(t, pool); len(left) != 0 {
		t.Errorf("the Up left %v", left)
	}
	if kept := column[string](t, pool, `SELECT nspname::text FROM pg_namespace WHERE nspname = 'zeroes'`); len(kept) != 1 {
		t.Error("the Up removed a schema that is not Zero's")
	}

	if _, err := schema.DownTo(ctx, pool, before, testKeys); err != nil {
		t.Fatalf("rolling back %d: %v", zeroRemoved, err)
	}
	publicationKeepsSecretsAndOwners(t, pool)
}

// zeroObjects is every object of Zero's in the database, named.
func zeroObjects(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	return column[string](t, pool, `
		SELECT 'publication ' || pubname FROM pg_publication WHERE pubname = 'zero_publication' OR pubname ~ '^_zero_metadata_'
		UNION ALL SELECT 'event trigger ' || evtname FROM pg_event_trigger WHERE evtname ~ '^zero_ddl_'
		UNION ALL SELECT 'slot ' || slot_name FROM pg_replication_slots WHERE slot_name ~ '^zero_[0-9]+_'
		UNION ALL SELECT 'schema ' || nspname FROM pg_namespace WHERE nspname ~ '^zero(_[0-9]+(/(cdc|cvr))?)?$'`)
}

// publicationKeepsSecretsAndOwners is what made zero_publication safe while
// it existed: zero-cache sent every published column of a row to whoever
// synced the row, so the column lists were a security boundary. A rollback
// must bring back that publication and no wider one.
func publicationKeepsSecretsAndOwners(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	published := column[string](t, pool, `
		SELECT tablename || '.' || attname FROM pg_publication_tables, unnest(attnames) AS attname
		WHERE pubname = 'zero_publication'`)
	if len(published) == 0 {
		t.Fatal("zero_publication publishes nothing")
	}
	for _, secret := range []string{
		"search_source_connectors.config", "user.email", "user.hashed_password", "user.id",
		"connections.api_key", "personal_access_tokens.token_hash", "refresh_tokens.token_hash",
	} {
		if slices.Contains(published, secret) {
			t.Errorf("zero_publication publishes %s", secret)
		}
	}
	throughParent := map[string]string{
		"new_chat_messages":  "new_chat_threads.user_id",
		"chat_session_state": "new_chat_threads.user_id",
		"automation_runs":    "automations.user_id",
	}
	for _, table := range column[string](t, pool, `SELECT tablename::text FROM pg_publication_tables WHERE pubname = 'zero_publication'`) {
		owner := table + ".user_id"
		if parent, ok := throughParent[table]; ok {
			owner = parent
		}
		if !slices.Contains(published, owner) {
			t.Errorf("zero_publication publishes %s without %s", table, owner)
		}
	}
}
