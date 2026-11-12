package schema_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	schema "git.danvics.com/danvics/soloclef/soloclef_schema"
	"git.danvics.com/danvics/soloclef/soloclef_schema/schemaversion"
)

// serverVariable names a throwaway Postgres server (pgvector/pgvector:pg17 is
// what the stack runs) as a URL whose user may create databases. Every test
// makes a database of its own on it and drops it afterwards, so the tests
// never see each other's rows and never touch a database that was already
// there. scripts/test_database.sh starts such a server, runs these tests
// and removes it.
const serverVariable = "SOLOCLEF_SCHEMA_TEST_SERVER_URL"

// emptyDatabase returns a pool on a new, empty database.
func emptyDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	server := os.Getenv(serverVariable)
	if server == "" {
		t.Skipf("%s is not set; this test needs a throwaway Postgres (scripts/test_database.sh starts one)", serverVariable)
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, server)
	if err != nil {
		t.Fatalf("opening the server: %v", err)
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	name := "schema_test_" + hex.EncodeToString(suffix)
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+name); err != nil {
		admin.Close()
		t.Fatalf("creating %s: %v", name, err)
	}
	address, err := url.Parse(server)
	if err != nil {
		t.Fatal(err)
	}
	address.Path = "/" + name
	pool, err := pgxpool.New(ctx, address.String())
	if err != nil {
		t.Fatalf("opening %s: %v", name, err)
	}
	t.Cleanup(func() {
		pool.Close()
		if _, err := admin.Exec(ctx, `DROP DATABASE `+name+` WITH (FORCE)`); err != nil {
			t.Errorf("dropping %s: %v", name, err)
		}
		admin.Close()
	})
	return pool
}

// migrated returns a pool on a database every migration has been applied to.
func migrated(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := emptyDatabase(t)
	if _, err := schema.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	return pool
}

func column[T any](t *testing.T, pool *pgxpool.Pool, sql string, args ...any) []T {
	t.Helper()
	rows, err := pool.Query(context.Background(), sql, args...)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	defer rows.Close()
	var values []T
	for rows.Next() {
		var value T
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return values
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// refused runs a statement the schema must not accept and checks that the
// named constraint is what stopped it, so the test cannot pass because of a
// typing mistake in its own SQL.
func refused(t *testing.T, pool *pgxpool.Pool, constraint, sql string, args ...any) {
	t.Helper()
	_, err := pool.Exec(context.Background(), sql, args...)
	if err == nil {
		t.Errorf("accepted, and %s should have refused it: %s", constraint, sql)
		return
	}
	if !strings.Contains(err.Error(), constraint) {
		t.Errorf("refused, but not by %s: %v", constraint, err)
	}
}

func TestMigrateBuildsAnEmptyDatabaseAndIsSafeToRepeat(t *testing.T) {
	pool := emptyDatabase(t)
	ctx := context.Background()

	// A database nothing has migrated is not one a service may start on.
	if err := schemaversion.Check(ctx, pool); err == nil || !strings.Contains(err.Error(), "never been migrated") {
		t.Fatalf("an empty database: %v", err)
	}

	expected, err := schemaversion.Expected()
	if err != nil {
		t.Fatal(err)
	}
	applied, err := schema.Migrate(ctx, pool)
	if err != nil {
		t.Fatalf("the first run: %v", err)
	}
	if len(applied) != len(expected) {
		t.Fatalf("the first run applied %d migrations and the build holds %d", len(applied), len(expected))
	}
	if err := schemaversion.Check(ctx, pool); err != nil {
		t.Fatalf("a freshly migrated database was refused: %v", err)
	}

	again, err := schema.Migrate(ctx, pool)
	if err != nil || len(again) != 0 {
		t.Fatalf("the second run applied %v, %v; it should find nothing to do", again, err)
	}

	listed, err := schema.Status(ctx, pool)
	if err != nil || len(listed) != len(expected) {
		t.Fatalf("status = %v, %v", listed, err)
	}
	for _, migration := range listed {
		if migration.Pending() {
			t.Errorf("%s is listed as pending after it ran", migration.Name)
		}
	}
}

func TestStatusOnAnUnmigratedDatabaseListsEverythingAsPending(t *testing.T) {
	pool := emptyDatabase(t)
	listed, err := schema.Status(context.Background(), pool)
	if err != nil || len(listed) == 0 {
		t.Fatalf("status = %v, %v", listed, err)
	}
	for _, migration := range listed {
		if !migration.Pending() {
			t.Errorf("%s is listed as applied to a database nothing was applied to", migration.Name)
		}
	}
	// Asking where a database stands must not build it.
	if tables := column[string](t, pool, `SELECT tablename::text FROM pg_tables WHERE schemaname = 'public' AND tablename <> $1`, schemaversion.Table); len(tables) != 0 {
		t.Errorf("status created %v", tables)
	}
}

func TestAServiceRefusesADatabaseAtAnotherVersion(t *testing.T) {
	ctx := context.Background()
	latest, err := schemaversion.Latest()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("behind", func(t *testing.T) {
		pool := migrated(t)
		// What a database looks like when the build is one migration ahead
		// of it: the newest version's row is not there.
		exec(t, pool, `DELETE FROM `+schemaversion.Table+` WHERE version_id = $1`, latest)
		err := schemaversion.Check(ctx, pool)
		if err == nil || !strings.Contains(err.Error(), "behind") || !strings.Contains(err.Error(), schemaversion.MigrateCommand) {
			t.Fatalf("a database behind the build: %v", err)
		}
	})

	t.Run("ahead", func(t *testing.T) {
		pool := migrated(t)
		// What a database looks like to a build older than it.
		exec(t, pool, `INSERT INTO `+schemaversion.Table+` (version_id, is_applied) VALUES ($1, true)`, latest+1)
		err := schemaversion.Check(ctx, pool)
		if err == nil || !strings.Contains(err.Error(), "older than the database") {
			t.Fatalf("a database ahead of the build: %v", err)
		}
	})
}

func TestRollingBackToZeroLeavesNothingAndTheMigrationsApplyAgain(t *testing.T) {
	pool := migrated(t)
	ctx := context.Background()
	expected, _ := schemaversion.Expected()

	rolled, err := schema.DownTo(ctx, pool, 0)
	if err != nil || len(rolled) != len(expected) {
		t.Fatalf("down to 0 rolled back %v, %v", rolled, err)
	}
	// Everything a migration created is gone: a Down section that forgets a
	// table or a type leaves a database the Up section cannot be run on.
	left := column[string](t, pool, `
		SELECT 'table ' || tablename FROM pg_tables WHERE schemaname = 'public' AND tablename <> $1
		UNION ALL
		SELECT 'type ' || t.typname FROM pg_type AS t JOIN pg_namespace AS n ON n.oid = t.typnamespace
		WHERE n.nspname = 'public' AND t.typtype = 'e'
		UNION ALL
		SELECT 'publication ' || pubname FROM pg_publication
		UNION ALL
		SELECT 'extension ' || extname FROM pg_extension WHERE extname <> 'plpgsql'`, schemaversion.Table)
	if len(left) != 0 {
		t.Fatalf("after rolling back to 0 the database still has: %s", strings.Join(left, ", "))
	}
	if err := schemaversion.Check(ctx, pool); err == nil {
		t.Fatal("a rolled-back database was accepted")
	}

	if _, err := schema.Migrate(ctx, pool); err != nil {
		t.Fatalf("applying the migrations again: %v", err)
	}
	if err := schemaversion.Check(ctx, pool); err != nil {
		t.Fatalf("after applying them again: %v", err)
	}
}

// ownedThroughAParent lists the tables that have no user_id of their own,
// each with the reason. A new table missing from here must carry the owner.
var ownedThroughAParent = map[string]string{
	"user":                       "the account itself",
	"site_settings":              "the operator's, not an account's",
	"site_announcements":         "the operator's, addressed to everyone",
	"push_tickets":               "keyed by a device's token; the provider's receipt names no account",
	"models":                     "owned through its connection, which is the operator's or one account's",
	"connections":                "user_id is NULL for the operator's own connection",
	"document_versions":          "owned through its document",
	"document_source_guides":     "owned through its document",
	"new_chat_messages":          "owned through its thread",
	"chat_session_state":         "owned through its thread",
	"artifact_files":             "owned through its artifact",
	"automation_triggers":        "owned through its automation",
	"automation_runs":            "owned through its automation",
	"soloclef_agent_checkpoints": "the agent library's table, keyed by a thread's name",
}

func TestEveryTableIsKeyedByItsOwner(t *testing.T) {
	pool := migrated(t)
	tables := column[string](t, pool, `SELECT tablename::text FROM pg_tables WHERE schemaname = 'public' AND tablename <> $1 ORDER BY 1`, schemaversion.Table)
	if len(tables) == 0 {
		t.Fatal("the migrations created no tables")
	}
	// A table is keyed by its owner when it has user_id uuid NOT NULL with a
	// foreign key to "user" that deletes the row with the account.
	keyed := column[string](t, pool, `
		SELECT c.relname::text
		FROM pg_constraint AS k
		JOIN pg_class AS c ON c.oid = k.conrelid
		JOIN pg_attribute AS a ON a.attrelid = c.oid AND a.attnum = k.conkey[1]
		WHERE k.contype = 'f' AND k.confrelid = '"user"'::regclass AND k.confdeltype = 'c'
		  AND cardinality(k.conkey) = 1 AND a.attname = 'user_id' AND a.attnotnull
		  AND a.atttypid = 'uuid'::regtype`)
	for _, table := range tables {
		_, excepted := ownedThroughAParent[table]
		switch {
		case slices.Contains(keyed, table) && excepted:
			t.Errorf("%s carries its owner and is also listed as owned through a parent: take it off the list", table)
		case !slices.Contains(keyed, table) && !excepted:
			t.Errorf(`%s has no "user_id uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE"`, table)
		}
	}
	for table := range ownedThroughAParent {
		if !slices.Contains(tables, table) {
			t.Errorf("%s is listed as owned through a parent and does not exist", table)
		}
	}
}

func TestNothingOfTheOldShapeIsLeft(t *testing.T) {
	pool := migrated(t)

	// Workspaces are gone from the database: no table, column, constraint,
	// index or type is named after one.
	named := column[string](t, pool, `
		SELECT 'table ' || relname FROM pg_class WHERE relnamespace = 'public'::regnamespace AND relname ILIKE '%workspace%'
		UNION ALL
		SELECT 'column ' || table_name || '.' || column_name FROM information_schema.columns
		WHERE table_schema = 'public' AND column_name ILIKE '%workspace%'
		UNION ALL
		SELECT 'constraint ' || conname FROM pg_constraint WHERE connamespace = 'public'::regnamespace AND conname ILIKE '%workspace%'
		UNION ALL
		SELECT 'type ' || typname FROM pg_type WHERE typnamespace = 'public'::regnamespace AND typname ILIKE '%workspace%'`)
	if len(named) != 0 {
		t.Errorf("still named after workspaces: %s", strings.Join(named, ", "))
	}

	// json stores a JSON null and SQL NULL as two things; vectors live in
	// Qdrant. Neither type has a column here.
	typed := column[string](t, pool, `
		SELECT table_name || '.' || column_name || ' ' || udt_name FROM information_schema.columns
		WHERE table_schema = 'public' AND udt_name IN ('json', 'vector')`)
	if len(typed) != 0 {
		t.Errorf("json or vector columns: %s", strings.Join(typed, ", "))
	}

	// The indexer writes Qdrant itself. A trigger on chunks was how the
	// Python tier mirrored them, and its drain deleted any point whose row
	// had no embedding, which is every row this schema can hold.
	triggers := column[string](t, pool, `
		SELECT c.relname || '.' || t.tgname FROM pg_trigger AS t JOIN pg_class AS c ON c.oid = t.tgrelid
		WHERE c.relnamespace = 'public'::regnamespace AND NOT t.tgisinternal`)
	if len(triggers) != 0 {
		t.Errorf("triggers: %s", strings.Join(triggers, ", "))
	}
}

// A foreign key with no index its lookup can use makes every delete of the
// referenced row a scan of the whole referencing table.
//
// "Can use" is: the index leads with the key's columns. For a (parent, owner)
// key an index that leads with the parent's id alone is enough, since an id
// already picks out the parent's rows; one that leads with the owner alone
// is not, because that is every row the account has. A partial index counts
// only when all it leaves out is the rows where the key is NULL.
func TestEveryForeignKeyHasAnIndex(t *testing.T) {
	pool := migrated(t)
	type index struct {
		columns   []string
		predicate string
	}
	indexes := map[string][]index{}
	rows, err := pool.Query(context.Background(), `
		SELECT c.relname::text,
		       ARRAY(SELECT a.attname::text FROM unnest(i.indkey::int2[]) WITH ORDINALITY AS k(attnum, position)
		             JOIN pg_attribute AS a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
		             ORDER BY k.position),
		       COALESCE(pg_get_expr(i.indpred, i.indrelid), '')
		FROM pg_index AS i JOIN pg_class AS c ON c.oid = i.indrelid
		WHERE c.relnamespace = 'public'::regnamespace AND i.indkey[0] <> 0`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var table string
		var found index
		if err := rows.Scan(&table, &found.columns, &found.predicate); err != nil {
			t.Fatal(err)
		}
		indexes[table] = append(indexes[table], found)
	}
	rows.Close()

	keys, err := pool.Query(context.Background(), `
		SELECT c.relname::text, k.conname::text,
		       ARRAY(SELECT a.attname::text FROM unnest(k.conkey) AS n JOIN pg_attribute AS a ON a.attrelid = k.conrelid AND a.attnum = n)
		FROM pg_constraint AS k JOIN pg_class AS c ON c.oid = k.conrelid
		WHERE k.contype = 'f' AND k.connamespace = 'public'::regnamespace
		ORDER BY 1, 2`)
	if err != nil {
		t.Fatal(err)
	}
	defer keys.Close()
	checked := 0
	for keys.Next() {
		var table, name string
		var columns []string
		if err := keys.Scan(&table, &name, &columns); err != nil {
			t.Fatal(err)
		}
		checked++
		usable := slices.ContainsFunc(indexes[table], func(candidate index) bool {
			if candidate.predicate != "" && !slices.ContainsFunc(columns, func(column string) bool {
				return candidate.predicate == "("+column+" IS NOT NULL)"
			}) {
				return false
			}
			// How many of the index's leading columns belong to the key.
			leading := 0
			for leading < len(candidate.columns) && slices.Contains(columns, candidate.columns[leading]) {
				leading++
			}
			switch {
			case leading == len(columns):
				return true
			case leading == 0:
				return false
			default:
				return !slices.Equal(candidate.columns[:leading], []string{"user_id"})
			}
		})
		if !usable {
			t.Errorf("%s: no index leads with the columns of %s (%s)", table, name, strings.Join(columns, ", "))
		}
	}
	if err := keys.Err(); err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("the schema has no foreign keys to check")
	}
}

const (
	ada = "aaaaaaaa-0000-4000-8000-000000000001"
	ben = "bbbbbbbb-0000-4000-8000-000000000002"
)

// twoAccounts seeds two accounts, each with a folder, a document in it, a
// chunk, a notebook, and a thread in that notebook.
func twoAccounts(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	exec(t, pool, `
		INSERT INTO "user" (id, email) VALUES ($1, 'ada@example.org'), ($2, 'ben@example.org');
	`, ada, ben)
	for index, owner := range []string{ada, ben} {
		id := int64(index+1) * 10
		exec(t, pool, `INSERT INTO folders (id, user_id, name, "position") VALUES ($1, $2, 'Biology', 'a0')`, id, owner)
		exec(t, pool, `INSERT INTO documents (id, user_id, title, document_type, content, content_hash, folder_id)
			VALUES ($1, $2, 'Cells', 'FILE', 'x', 'h', $1)`, id, owner)
		exec(t, pool, `INSERT INTO chunks (id, document_id, user_id, content) VALUES ($1, $1, $2, 'Mitosis has four phases.')`, id, owner)
		exec(t, pool, `INSERT INTO notebooks (id, user_id, title) VALUES ($1, $2, 'Cells')`, id, owner)
		exec(t, pool, `INSERT INTO new_chat_threads (id, user_id, title, notebook_id) VALUES ($1, $2, 'Chat', $1)`, id, owner)
	}
}

func TestARowCannotPointAtAnotherAccountsRow(t *testing.T) {
	pool := migrated(t)
	twoAccounts(t, pool)

	// Ada's rows are 10 and Ben's are 20. Each statement names Ben as the
	// owner and one of Ada's rows as the parent.
	refused(t, pool, "chunks_document_owner_fkey",
		`INSERT INTO chunks (document_id, user_id, content) VALUES (10, $1, 'x')`, ben)
	refused(t, pool, "document_files_document_owner_fkey",
		`INSERT INTO document_files (document_id, user_id, storage_backend, storage_key, original_filename, size_bytes)
		 VALUES (10, $1, 's3', 'k', 'f.pdf', 1)`, ben)
	refused(t, pool, "documents_folder_owner_fkey",
		`INSERT INTO documents (user_id, title, document_type, content, content_hash, folder_id) VALUES ($1, 't', 'FILE', 'x', 'h', 10)`, ben)
	refused(t, pool, "folders_parent_owner_fkey",
		`INSERT INTO folders (user_id, parent_id, name, "position") VALUES ($1, 10, 'Inside', 'a0')`, ben)
	refused(t, pool, "notebook_sources_document_owner_fkey",
		`INSERT INTO notebook_sources (notebook_id, user_id, document_id) VALUES (20, $1, 10)`, ben)
	refused(t, pool, "notebook_sources_folder_owner_fkey",
		`INSERT INTO notebook_sources (notebook_id, user_id, folder_id) VALUES (20, $1, 10)`, ben)
	refused(t, pool, "notebook_sources_notebook_owner_fkey",
		`INSERT INTO notebook_sources (notebook_id, user_id, document_id) VALUES (10, $1, 20)`, ben)
	refused(t, pool, "new_chat_threads_notebook_owner_fkey",
		`INSERT INTO new_chat_threads (user_id, title, notebook_id) VALUES ($1, 'Chat', 10)`, ben)
	refused(t, pool, "notes_document_owner_fkey",
		`INSERT INTO notes (document_id, user_id) VALUES (10, $1)`, ben)
	refused(t, pool, "notes_notebook_owner_fkey",
		`INSERT INTO notes (document_id, user_id, notebook_id) VALUES (20, $1, 10)`, ben)
	refused(t, pool, "artifacts_document_owner_fkey",
		`INSERT INTO artifacts (document_id, user_id, format) VALUES (10, $1, 'quiz')`, ben)
	refused(t, pool, "notebook_outputs_notebook_owner_fkey",
		`INSERT INTO notebook_outputs (notebook_id, user_id, kind, title) VALUES (10, $1, 'quiz', 't')`, ben)

	// The same file may be two accounts' document, and is one document
	// within an account.
	exec(t, pool, `UPDATE documents SET unique_identifier_hash = 'same-file'`)
	refused(t, pool, "uq_documents_owner_identity",
		`INSERT INTO documents (user_id, title, document_type, content, content_hash, unique_identifier_hash)
		 VALUES ($1, 't', 'FILE', 'x', 'h', 'same-file')`, ada)
}

func TestTheConstraintsThatHadHoles(t *testing.T) {
	pool := migrated(t)
	twoAccounts(t, pool)

	// Two folders of one name at the top level, where the parent is NULL.
	refused(t, pool, "uq_folders_owner_parent_name",
		`INSERT INTO folders (user_id, name, "position") VALUES ($1, 'Biology', 'a1')`, ada)
	// The same name is free under another parent and for another account.
	exec(t, pool, `INSERT INTO folders (user_id, parent_id, name, "position") VALUES ($1, 10, 'Biology', 'a0')`, ada)

	// An account-wide rule (no thread), stored twice.
	exec(t, pool, `INSERT INTO agent_permission_rules (user_id, permission, action) VALUES ($1, 'edit', 'allow')`, ada)
	refused(t, pool, "uq_agent_permission_rules_scope",
		`INSERT INTO agent_permission_rules (user_id, permission, action) VALUES ($1, 'edit', 'allow')`, ada)
	refused(t, pool, "ck_agent_permission_rules_action",
		`INSERT INTO agent_permission_rules (user_id, permission, action) VALUES ($1, 'edit', 'maybe')`, ada)

	// Two accounts whose addresses differ only by case.
	refused(t, pool, "uq_user_email_lower", `INSERT INTO "user" (email) VALUES ('ADA@example.org')`)

	// A status that is not an object naming a state.
	for _, status := range []string{`[]`, `{}`, `"ready"`, `{"state": 1}`} {
		refused(t, pool, "ck_documents_status", `UPDATE documents SET status = $1::jsonb WHERE id = 10`, status)
	}
	// A model choice for a purpose nothing reads, and an operator's
	// connection that claims an owner.
	refused(t, pool, "ck_user_models_purpose", `INSERT INTO user_models (user_id, purpose, model) VALUES ($1, 'poetry', 'm')`, ada)
	refused(t, pool, "ck_connections_scope_owner", `INSERT INTO connections (provider, scope, user_id) VALUES ('openai', 'global', $1)`, ada)
	refused(t, pool, "ck_connections_scope_owner", `INSERT INTO connections (provider, scope) VALUES ('openai', 'user')`)
}

func TestWhatADeleteTakesWithItAndWhatItLeaves(t *testing.T) {
	pool := migrated(t)
	twoAccounts(t, pool)
	one := func(sql string, args ...any) int64 {
		t.Helper()
		values := column[*int64](t, pool, sql, args...)
		if len(values) != 1 {
			t.Fatalf("%s returned %d rows", sql, len(values))
		}
		if values[0] == nil {
			return -1 // SQL NULL
		}
		return *values[0]
	}

	// A note in Ada's notebook, kept from an answer in its chat.
	exec(t, pool, `INSERT INTO documents (id, user_id, title, document_type, content, content_hash) VALUES (11, $1, 'A thought', 'NOTE', 'x', 'h')`, ada)
	exec(t, pool, `INSERT INTO new_chat_messages (id, thread_id, role, content) VALUES (5, 10, 'ASSISTANT', '[]')`)
	exec(t, pool, `INSERT INTO notes (document_id, user_id, notebook_id, origin, thread_id, message_id) VALUES (11, $1, 10, 'chat', 10, 5)`, ada)
	exec(t, pool, `INSERT INTO notebook_sources (notebook_id, user_id, document_id) VALUES (10, $1, 11)`, ada)

	// Deleting a folder leaves its documents, loose, and still their owner's.
	exec(t, pool, `DELETE FROM folders WHERE id = 10`)
	if folder := one(`SELECT folder_id FROM documents WHERE id = 10 AND user_id = $1`, ada); folder != -1 {
		t.Errorf("after its folder was deleted the document is in folder %d", folder)
	}

	// Deleting a notebook deletes its source links, and leaves its notes
	// (unfiled) and its chat (as a plain chat).
	exec(t, pool, `DELETE FROM notebooks WHERE id = 10`)
	if notebook := one(`SELECT notebook_id FROM notes WHERE document_id = 11 AND user_id = $1`, ada); notebook != -1 {
		t.Errorf("after its notebook was deleted the note is filed in %d", notebook)
	}
	if notebook := one(`SELECT notebook_id FROM new_chat_threads WHERE id = 10 AND user_id = $1`, ada); notebook != -1 {
		t.Errorf("after its notebook was deleted the thread is in %d", notebook)
	}
	if sources := one(`SELECT count(*) FROM notebook_sources WHERE notebook_id = 10`); sources != 0 {
		t.Errorf("%d source links outlived their notebook", sources)
	}

	// Deleting the answer a note was kept from leaves the note.
	exec(t, pool, `DELETE FROM new_chat_messages WHERE id = 5`)
	if message := one(`SELECT message_id FROM notes WHERE document_id = 11`); message != -1 {
		t.Errorf("the note still names message %d", message)
	}

	// Deleting a document deletes its chunks and its note row.
	exec(t, pool, `DELETE FROM documents WHERE id = 11`)
	if notes := one(`SELECT count(*) FROM notes WHERE document_id = 11`); notes != 0 {
		t.Error("a note outlived its document")
	}

	// Deleting an account deletes every row it owned and none of anyone
	// else's.
	exec(t, pool, `DELETE FROM "user" WHERE id = $1`, ada)
	for _, table := range column[string](t, pool, `
		SELECT table_name::text FROM information_schema.columns
		WHERE table_schema = 'public' AND column_name = 'user_id' ORDER BY 1`) {
		if rows := one(`SELECT count(*) FROM `+table+` WHERE user_id = $1`, ada); rows != 0 {
			t.Errorf("%s still has %d of the deleted account's rows", table, rows)
		}
	}
	if chunks := one(`SELECT count(*) FROM chunks WHERE user_id = $1`, ben); chunks != 1 {
		t.Errorf("the other account has %d chunks, not the 1 it had", chunks)
	}
}

// zero-cache sends every published column of a row to whoever syncs the row,
// so what the publication lists is a security boundary.
func TestThePublicationSendsNoSecretsAndCarriesTheOwner(t *testing.T) {
	pool := migrated(t)
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
	// Each published table says whose a row is, itself or through a parent
	// that is published with its owner.
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
			t.Errorf("zero_publication publishes %s without %s, so a client cannot be limited to its own rows", table, owner)
		}
	}
}
