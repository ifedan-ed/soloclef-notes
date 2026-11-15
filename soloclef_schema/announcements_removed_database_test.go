package schema_test

import (
	"context"
	"testing"

	schema "git.danvics.com/danvics/soloclef/soloclef_schema"
)

// announcementsRemoved is 00015, which took the administrator's
// announcements out of the schema.
const announcementsRemoved = 15

// Migration 15 deletes every announcement's inbox copy and its saved push
// switch, leaves every other inbox row and choice as it was, and drops the
// table. Rolled back, the table is back as 00001 made it, and empty: what
// was sent cannot come back.
func TestAnnouncementsLeaveTheInboxAndTheSchema(t *testing.T) {
	pool := migrated(t)
	ctx := context.Background()
	const ada = "11111111-1111-4111-8111-111111111111"
	table := func() []string {
		return column[string](t, pool, `SELECT coalesce(to_regclass('site_announcements')::text, '')`)
	}
	if left := table(); left[0] != "" {
		t.Fatalf("the migrated schema still has %s", left[0])
	}

	if _, err := schema.DownTo(ctx, pool, versionBefore(t, announcementsRemoved), testKeys); err != nil {
		t.Fatalf("rolling back to %d: %v", versionBefore(t, announcementsRemoved), err)
	}
	exec(t, pool, `INSERT INTO "user" (id, email) VALUES ($1, 'a@example.test')`, ada)
	exec(t, pool, `INSERT INTO site_announcements (id, title, body, created_by_id) VALUES (7, 'Maintenance', 'Tonight', $1)`, ada)
	exec(t, pool, `INSERT INTO notifications (user_id, type, title, message, metadata) VALUES
		($1, 'site_announcement', 'Maintenance', 'Tonight', '{"announcement_id": 7}'),
		($1, 'document_processing', 'Tides.pdf', 'Indexed', '{"status": "completed"}')`, ada)
	exec(t, pool, `INSERT INTO push_preferences (user_id, choices) VALUES ($1, '{"announcement": false, "output": false}')`, ada)

	if _, err := schema.UpTo(ctx, pool, announcementsRemoved, testKeys); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	if kept := column[string](t, pool, `SELECT type FROM notifications ORDER BY id`); !equal(kept, []string{"document_processing"}) {
		t.Errorf("after the Up the inbox holds %v, want only the document's row", kept)
	}
	if kept := column[string](t, pool, `SELECT choices::text FROM push_preferences`); !equal(kept, []string{`{"output": false}`}) {
		t.Errorf("after the Up the saved choices are %v", kept)
	}
	if left := table(); left[0] != "" {
		t.Errorf("the Up left %s", left[0])
	}

	if _, err := schema.DownTo(ctx, pool, versionBefore(t, announcementsRemoved), testKeys); err != nil {
		t.Fatalf("rolling back %d: %v", announcementsRemoved, err)
	}
	shape := column[string](t, pool, `
		SELECT 'column ' || column_name || ' ' || data_type || ' ' || is_nullable || ' ' || coalesce(column_default, '')
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'site_announcements'
		UNION ALL
		SELECT 'index ' || indexname FROM pg_indexes
		WHERE schemaname = 'public' AND tablename = 'site_announcements'
		UNION ALL
		SELECT 'foreign key ' || confdeltype::text FROM pg_constraint
		WHERE conrelid = 'site_announcements'::regclass AND contype = 'f'
		ORDER BY 1`)
	want := []string{
		"column body text NO ",
		"column cleared_read_count integer NO 0",
		"column created_at timestamp with time zone NO now()",
		"column created_by_id uuid YES ",
		"column expires_at timestamp with time zone YES ",
		"column id bigint NO ",
		"column link character varying YES ",
		"column recipient_count integer NO 0",
		"column title character varying NO ",
		"column withdrawn_at timestamp with time zone YES ",
		// "n" is ON DELETE SET NULL: an account removed leaves its
		// announcement standing, unsigned.
		"foreign key n",
		"index ix_site_announcements_created_by_id",
		"index site_announcements_pkey",
	}
	if !equal(shape, want) {
		t.Errorf("the Down brought back\n%v\nwant\n%v", shape, want)
	}
	if empty := column[int64](t, pool, `SELECT count(*) FROM site_announcements`); empty[0] != 0 {
		t.Errorf("the Down brought back %d announcements", empty[0])
	}

	if _, err := schema.Migrate(ctx, pool, testKeys); err != nil {
		t.Fatalf("migrating again: %v", err)
	}
	if left := table(); left[0] != "" {
		t.Errorf("migrating again left %s", left[0])
	}
}
