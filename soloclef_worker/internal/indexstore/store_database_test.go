package indexstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// databaseEnv names a throwaway Postgres that soloclef_schema's migrate
// command has brought to the current schema. It is the
// variable scripts/index_end_to_end_test.sh sets for the end-to-end tests,
// so the same run covers this one. Without it the test is skipped: a plain
// `go test ./...` touches no database.
const databaseEnv = "SOLOCLEF_INDEX_TEST_DATABASE_URL"

func token(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(raw)
}

// Load against real rows: the SQL joins the document's connection, and the
// parser it reports is the document's own, else the connection's, else none
// (the deployment's default). This is the half chooseParser's unit test
// cannot show: that the join reads the right row and a document with no
// connection still loads.
func TestLoadAppliesTheConnectorsParserWhenTheDocumentNamesNone(t *testing.T) {
	address := os.Getenv(databaseEnv)
	if address == "" {
		t.Skipf("%s is not set; scripts/index_end_to_end_test.sh starts the database this test needs", databaseEnv)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := New(pool)

	var userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO "user" (email, hashed_password, is_verified)
		VALUES ($1, 'not a password', true)
		RETURNING id::text`, "parser-"+token(t)+"@example.test").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	// Everything this test wrote goes with the account: its documents and
	// its connections cascade from it.
	defer func() {
		if _, err := pool.Exec(ctx, `DELETE FROM "user" WHERE id = $1::uuid`, userID); err != nil {
			t.Errorf("cleaning up: %v", err)
		}
	}()

	connector := func(parser *string) int64 {
		t.Helper()
		var id int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO search_source_connectors (name, connector_type, is_indexable, config, user_id, parser)
			VALUES ($1, 'NEXTCLOUD_CONNECTOR', true, '{}', $2::uuid, $3)
			RETURNING id`, "Parser test "+token(t), userID, parser).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	document := func(parser *string, connectorID *int64) int64 {
		t.Helper()
		var id int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO documents (title, document_type, content, content_hash, user_id,
			                       status, parser, connector_id, source_markdown)
			VALUES ('parser.md', 'FILE', '', $1, $2::uuid, '{"state": "pending"}', $3, $4, '# text')
			RETURNING id`, token(t)+token(t), userID, parser, connectorID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	name := func(text string) *string { return &text }
	withParser, withoutParser := connector(name("datalab")), connector(nil)

	for _, test := range []struct {
		about     string
		document  *string
		connector *int64
		parser    string
		from      ParserSource
	}{
		{"no connection, no parser", nil, nil, "", ParserFromDefault},
		{"no connection, its own parser", name("llamaparse"), nil, "llamaparse", ParserFromDocument},
		{"the connection's parser, the document naming none", nil, &withParser, "datalab", ParserFromConnector},
		{"its own parser over the connection's", name("unstructured"), &withParser, "unstructured", ParserFromDocument},
		{"a connection that names none", nil, &withoutParser, "", ParserFromDefault},
	} {
		loaded, err := store.Load(ctx, document(test.document, test.connector))
		if err != nil {
			t.Fatalf("%s: %v", test.about, err)
		}
		if loaded.UserID != userID {
			t.Errorf("%s: loaded owner %q, want %q", test.about, loaded.UserID, userID)
		}
		if loaded.Parser != test.parser || loaded.ParserFrom != test.from {
			t.Errorf("%s: loaded parser %q from %q, want %q from %q",
				test.about, loaded.Parser, loaded.ParserFrom, test.parser, test.from)
		}
	}
}

// Replace against the real chunk table: a new chunk is written under its
// document's owner, and the table's (document, owner) key refuses a chunk
// filed under any other account, so a chunk can never name one account and
// another's document.
func TestReplaceWritesChunksUnderTheDocumentsOwnerAndNoOther(t *testing.T) {
	address := os.Getenv(databaseEnv)
	if address == "" {
		t.Skipf("%s is not set; scripts/index_end_to_end_test.sh starts the database this test needs", databaseEnv)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	// Registered first so it runs last: the accounts below are deleted
	// through this pool by their own clean-ups.
	t.Cleanup(pool.Close)
	store := New(pool)

	account := func() string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO "user" (email, hashed_password, is_verified) VALUES ($1, 'not a password', true)
			RETURNING id::text`, "replace-"+token(t)+"@example.test").Scan(&id); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := pool.Exec(ctx, `DELETE FROM "user" WHERE id = $1::uuid`, id); err != nil {
				t.Errorf("cleaning up: %v", err)
			}
		})
		return id
	}
	owner, stranger := account(), account()
	var documentID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO documents (title, document_type, content, content_hash, user_id, status)
		VALUES ('owned.md', 'FILE', '', $1, $2::uuid, '{"state": "processing"}')
		RETURNING id`, token(t)+token(t), owner).Scan(&documentID); err != nil {
		t.Fatal(err)
	}

	ids, err := store.AllocateChunkIDs(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	place := Place{Position: 0, StartLine: 1, EndLine: 1}
	_, err = store.Replace(ctx, Replacement{
		DocumentID: documentID, UserID: stranger, Markdown: "Text.",
		New: []NewChunk{{ID: ids[0], Content: "Text.", Place: place}},
	})
	if err == nil || !strings.Contains(err.Error(), "chunks_document_owner_fkey") {
		t.Fatalf("a chunk filed under another account: err = %v, want the foreign key to refuse it", err)
	}

	if _, err := store.Replace(ctx, Replacement{
		DocumentID: documentID, UserID: owner, Markdown: "Text.",
		New: []NewChunk{{ID: ids[1], Content: "Text.", Place: place}},
	}); err != nil {
		t.Fatal(err)
	}
	var filedUnder, state string
	if err := pool.QueryRow(ctx, `
		SELECT c.user_id::text, d.status ->> 'state'
		FROM chunks AS c JOIN documents AS d ON d.id = c.document_id
		WHERE c.id = $1`, ids[1]).Scan(&filedUnder, &state); err != nil {
		t.Fatal(err)
	}
	if filedUnder != owner || state != "ready" {
		t.Errorf("the chunk is filed under %q in a %s document, want %q and ready", filedUnder, state, owner)
	}
}

// Replace for a note: what was indexed is written as the content, but the
// note's own Markdown is never overwritten. A save that landed while the job
// ran keeps its text and its pending state (its own job is queued), and a
// note nobody touched meanwhile is marked ready.
func TestReplaceNeverOverwritesANoteSavedWhileItWasIndexed(t *testing.T) {
	address := os.Getenv(databaseEnv)
	if address == "" {
		t.Skipf("%s is not set; scripts/index_end_to_end_test.sh starts the database this test needs", databaseEnv)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store := New(pool)
	var owner string
	if err := pool.QueryRow(ctx, `
		INSERT INTO "user" (email, hashed_password, is_verified) VALUES ($1, 'not a password', true)
		RETURNING id::text`, "note-"+token(t)+"@example.test").Scan(&owner); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM "user" WHERE id = $1::uuid`, owner) })

	note := func(markdown, state string) int64 {
		t.Helper()
		var id int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO documents (title, document_type, content, content_hash, source_markdown, user_id, status)
			VALUES ('A note', 'NOTE', $1, $2, $1, $3::uuid, jsonb_build_object('state', $4::text))
			RETURNING id`, markdown, token(t), owner, state).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	read := func(id int64) (content, markdown, state string, again bool) {
		t.Helper()
		if err := pool.QueryRow(ctx, `
			SELECT content, source_markdown, status ->> 'state', content_needs_reindexing
			FROM documents WHERE id = $1`, id).Scan(&content, &markdown, &state, &again); err != nil {
			t.Fatal(err)
		}
		return
	}
	place := Place{Position: 0, StartLine: 1, EndLine: 1}

	// Saved while the job ran: the job indexed "First.", the person has
	// since written "First. Second." and the save marked the note pending.
	saved := note("First. Second.", "pending")
	ids, err := store.AllocateChunkIDs(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Replace(ctx, Replacement{
		DocumentID: saved, UserID: owner, Markdown: "First.", FromSourceMarkdown: true,
		New: []NewChunk{{ID: ids[0], Content: "First.", Place: place}},
	}); err != nil {
		t.Fatal(err)
	}
	if content, markdown, state, again := read(saved); content != "First." || markdown != "First. Second." || state != "pending" || !again {
		t.Errorf("a note saved mid-index: content %q, markdown %q, state %s, needs reindexing %v", content, markdown, state, again)
	}

	// Untouched while the job ran.
	untouched := note("Only.", "processing")
	if _, err := store.Replace(ctx, Replacement{
		DocumentID: untouched, UserID: owner, Markdown: "Only.", FromSourceMarkdown: true,
		New: []NewChunk{{ID: ids[1], Content: "Only.", Place: place}},
	}); err != nil {
		t.Fatal(err)
	}
	if content, markdown, state, again := read(untouched); content != "Only." || markdown != "Only." || state != "ready" || again {
		t.Errorf("an untouched note: content %q, markdown %q, state %s, needs reindexing %v", content, markdown, state, again)
	}
}

func TestLoadReadsTheVisionSwitchAndTheOwnersVisionChoice(t *testing.T) {
	address := os.Getenv(databaseEnv)
	if address == "" {
		t.Skipf("%s is not set; scripts/index_end_to_end_test.sh starts the database this test needs", databaseEnv)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := New(pool)

	account := func() string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO "user" (email, hashed_password, is_verified)
			VALUES ($1, 'not a password', true)
			RETURNING id::text`, "vision-"+token(t)+"@example.test").Scan(&id); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM "user" WHERE id = $1::uuid`, id) })
		return id
	}
	document := func(userID string, vision bool) int64 {
		t.Helper()
		var id int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO documents (title, document_type, content, content_hash, user_id, status, use_vision_llm)
			VALUES ('scan.pdf', 'FILE', '', $1, $2::uuid, '{"state": "pending"}', $3)
			RETURNING id`, token(t)+token(t), userID, vision).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}

	chooser, other := account(), account()
	if _, err := pool.Exec(ctx, `INSERT INTO user_models (user_id, purpose, model) VALUES ($1::uuid, 'vision', 'account-vision')`, chooser); err != nil {
		t.Fatal(err)
	}
	// The other account's choice is not this document's, and a chat choice
	// is not a vision one.
	if _, err := pool.Exec(ctx, `INSERT INTO user_models (user_id, purpose, model) VALUES ($1::uuid, 'chat', 'chat-model')`, other); err != nil {
		t.Fatal(err)
	}

	for name, case_ := range map[string]struct {
		id         int64
		vision     bool
		visionUsed string
	}{
		"vision on, the owner chose":      {document(chooser, true), true, "account-vision"},
		"vision off, the owner chose":     {document(chooser, false), false, "account-vision"},
		"vision on, the owner chose none": {document(other, true), true, ""},
	} {
		loaded, err := store.Load(ctx, case_.id)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if loaded.Vision != case_.vision || loaded.VisionChoice != case_.visionUsed {
			t.Errorf("%s: vision %v, choice %q", name, loaded.Vision, loaded.VisionChoice)
		}
	}
}
