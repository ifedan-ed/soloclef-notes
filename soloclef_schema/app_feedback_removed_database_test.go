package schema_test

import (
	"context"
	"testing"

	schema "git.danvics.com/danvics/soloclef/soloclef_schema"
)

// appFeedbackRemoved is 00021, which took app feedback out of the schema.
const appFeedbackRemoved = 21

// Migration 21 drops app_feedback with every rating in it. Rolled back, the
// table is back as 00018 made it (its columns, checks, indexes and the
// cascade to the account), and empty: what was sent cannot come back.
func TestAppFeedbackLeavesTheSchema(t *testing.T) {
	pool := migrated(t)
	ctx := context.Background()
	const ada = "11111111-1111-4111-8111-111111111111"
	table := func() string {
		return column[string](t, pool, `SELECT coalesce(to_regclass('app_feedback')::text, '')`)[0]
	}
	if left := table(); left != "" {
		t.Fatalf("the migrated schema still has %s", left)
	}

	before := versionBefore(t, appFeedbackRemoved)
	if _, err := schema.DownTo(ctx, pool, before, testKeys); err != nil {
		t.Fatalf("rolling back to %d: %v", before, err)
	}
	exec(t, pool, `INSERT INTO "user" (id, email) VALUES ($1, 'a@example.test')`, ada)
	exec(t, pool, `INSERT INTO app_feedback (app, platform, app_version, stars, comment, contact_ok, user_id)
		VALUES ('soloclef', 'web', '1.0.0', 5, 'Lovely', true, $1),
		       ('pedshub', 'ios', '2.1.0', 3, '', false, NULL)`, ada)

	if _, err := schema.UpTo(ctx, pool, appFeedbackRemoved, testKeys); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	if left := table(); left != "" {
		t.Errorf("the Up left %s", left)
	}
	if kept := column[int64](t, pool, `SELECT count(*) FROM "user" WHERE id = $1`, ada); kept[0] != 1 {
		t.Errorf("the Up took the account with the table")
	}

	if _, err := schema.DownTo(ctx, pool, before, testKeys); err != nil {
		t.Fatalf("rolling back %d: %v", appFeedbackRemoved, err)
	}
	shape := column[string](t, pool, `
		SELECT 'column ' || column_name || ' ' || data_type || ' ' || is_nullable || ' ' || coalesce(column_default, '')
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'app_feedback'
		UNION ALL
		SELECT 'index ' || indexname FROM pg_indexes
		WHERE schemaname = 'public' AND tablename = 'app_feedback'
		UNION ALL
		SELECT contype::text || ' ' || conname || ' ' || confdeltype::text FROM pg_constraint
		WHERE conrelid = 'app_feedback'::regclass AND contype IN ('c', 'f')
		ORDER BY 1`)
	want := []string{
		// A check has no delete action (a blank); the foreign key's "c" is
		// ON DELETE CASCADE: an erased account takes its feedback with it.
		"c ck_app_feedback_app  ",
		"c ck_app_feedback_app_version  ",
		"c ck_app_feedback_comment  ",
		"c ck_app_feedback_platform  ",
		"c ck_app_feedback_stars  ",
		"c ck_app_feedback_user_is_soloclefs  ",
		"column app text NO ",
		"column app_version text NO ",
		"column comment text NO ''::text",
		"column contact_ok boolean NO ",
		"column created_at timestamp with time zone NO now()",
		"column id bigint NO ",
		"column platform text NO ",
		"column stars smallint NO ",
		"column user_id uuid YES ",
		"f app_feedback_user_id_fkey c",
		"index app_feedback_pkey",
		"index ix_app_feedback_newest",
		"index ix_app_feedback_user_id",
	}
	if !equal(shape, want) {
		t.Errorf("the Down brought back\n%q\nwant\n%q", shape, want)
	}
	if empty := column[int64](t, pool, `SELECT count(*) FROM app_feedback`); empty[0] != 0 {
		t.Errorf("the Down brought back %d ratings", empty[0])
	}

	if _, err := schema.Migrate(ctx, pool, testKeys); err != nil {
		t.Fatalf("migrating again: %v", err)
	}
	if left := table(); left != "" {
		t.Errorf("migrating again left %s", left)
	}
}
