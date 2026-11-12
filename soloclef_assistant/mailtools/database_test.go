package mailtools

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The opt-in test below asks a real Postgres what the rest of the package
// asks a stand-in. It is skipped unless the variable names a throwaway
// database at the schema soloclef_schema's `migrate up` makes; it must
// never be pointed at one anybody uses, because it empties it.
//
//	SOLOCLEF_API_TEST_MAILTOOLS_DATABASE=postgres://check:check@127.0.0.1:39827/check \
//	  go test ./mailtools/ -run OverARealDatabase -count=1 -v
const databaseVariable = "SOLOCLEF_API_TEST_MAILTOOLS_DATABASE"

const (
	adaID = "aaaaaaaa-0000-4000-8000-000000000001"
	benID = "bbbbbbbb-0000-4000-8000-000000000002"
)

// Ada has a mail connection with a password mailbox and a Microsoft one,
// and a second mail connection she switched off. Ben has one Microsoft
// mailbox whose id is the same as Ada's, which is the case an id alone
// could not tell apart.
const seed = `
TRUNCATE "user", site_settings CASCADE;
INSERT INTO "user" (id, email) VALUES
  ('aaaaaaaa-0000-4000-8000-000000000001', 'ada@example.org'),
  ('bbbbbbbb-0000-4000-8000-000000000002', 'ben@example.org');
INSERT INTO search_source_connectors (id, name, connector_type, config, user_id, enabled, parser) VALUES
  (11, 'Email (IMAP)', 'EMAIL_IMAP_CONNECTOR', '{"accounts": [{"id": "pw", "label": "Home"}, {"id": "ms", "auth": "microsoft", "label": "Work", "refresh_token": "ada-refresh"}]}', 'aaaaaaaa-0000-4000-8000-000000000001', true, 'datalab'),
  (12, 'Email (IMAP), an older one', 'EMAIL_IMAP_CONNECTOR', '{"accounts": [{"id": "off", "label": "Old"}]}', 'aaaaaaaa-0000-4000-8000-000000000001', false, 'llamaparse'),
  (13, 'Not mail', 'MCP_CONNECTOR', '{"accounts": [{"id": "x"}]}', 'aaaaaaaa-0000-4000-8000-000000000001', true, 'unstructured'),
  (20, 'Email (IMAP)', 'EMAIL_IMAP_CONNECTOR', '{"accounts": [{"id": "ms", "auth": "microsoft", "label": "Ben''s", "refresh_token": "ben-refresh"}]}', 'bbbbbbbb-0000-4000-8000-000000000002', true, NULL);
`

func TestTheMailboxRowsOverARealDatabase(t *testing.T) {
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
	labels := func(ownerID string) (out []string) {
		t.Helper()
		records, err := store.Mailboxes(ctx, ownerID)
		if err != nil {
			t.Fatal(err)
		}
		for _, raw := range records {
			record := map[string]any{}
			_ = json.Unmarshal(raw, &record)
			label, _ := record["label"].(string)
			if token, _ := record["access_token"].(string); token != "" {
				label += "+" + token
			}
			out = append(out, label)
		}
		return out
	}
	join := func(list []string) string {
		text := ""
		for _, item := range list {
			text += item + "|"
		}
		return text
	}

	// An account's mailboxes are the ones of its own connection that is
	// switched on, in the order they were added, and nobody else's.
	if got := join(labels(adaID)); got != "Home|Work|" {
		t.Errorf("Ada's mailboxes: %s", got)
	}
	if got := join(labels(benID)); got != "Ben's|" {
		t.Errorf("Ben's mailboxes: %s", got)
	}
	if got := labels("cccccccc-0000-4000-8000-000000000003"); len(got) != 0 {
		t.Errorf("an account with no connection has %v", got)
	}

	// A refreshed token is written into the owner's own Microsoft mailbox
	// and no other record: not Ben's of the same id, not Ada's password
	// mailbox, and the others keep their place.
	changed, err := store.Merge(ctx, adaID, "ms", []byte(`{"access_token": "ada-new"}`))
	if err != nil || !changed {
		t.Fatalf("merging into Ada's mailbox: %v %v", changed, err)
	}
	if got := join(labels(adaID)); got != "Home|Work+ada-new|" {
		t.Errorf("Ada's mailboxes after the merge: %s", got)
	}
	if got := join(labels(benID)); got != "Ben's|" {
		t.Errorf("Ben's mailboxes after Ada's merge: %s", got)
	}
	// A password mailbox, a mailbox that is not there and another
	// account's are each "no such mailbox".
	for _, id := range []string{"pw", "nowhere", "off"} {
		if changed, err := store.Merge(ctx, adaID, id, []byte(`{"access_token": "x"}`)); err != nil || changed {
			t.Errorf("merging into %q: %v %v", id, changed, err)
		}
	}
	if changed, err := store.Merge(ctx, "cccccccc-0000-4000-8000-000000000003", "ms", []byte(`{"access_token": "x"}`)); err != nil || changed {
		t.Errorf("a third account merged into somebody's mailbox: %v %v", changed, err)
	}
	if got := join(labels(benID)); got != "Ben's|" {
		t.Errorf("Ben's mailboxes at the end: %s", got)
	}

	// The parser an attachment is read with is the one the owner's mail
	// connection that is switched on chose; none is the default ("").
	for ownerID, want := range map[string]string{adaID: "datalab", benID: "", "cccccccc-0000-4000-8000-000000000003": ""} {
		if got, err := store.Parser(ctx, ownerID); err != nil || got != want {
			t.Errorf("Parser(%s) = %q, %v; want %q", ownerID, got, err, want)
		}
	}
}
