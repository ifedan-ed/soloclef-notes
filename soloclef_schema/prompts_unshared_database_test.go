package schema_test

import (
	"context"
	"testing"

	schema "git.danvics.com/danvics/soloclef/soloclef_schema"
)

// promptsUnshared is 00015, which took the sharing flag off prompts.
const promptsUnshared = 15

// Prompts are one person's own, so the schema has no way to share one: no
// is_public column and no index for a community shelf. The Down brings both
// back as 00001 made them, every prompt private, and the Up takes them away
// again without touching the prompts themselves.
func TestPromptsHaveNoSharingFlag(t *testing.T) {
	pool := migrated(t)
	ctx := context.Background()
	sharing := func() []string {
		return column[string](t, pool, `
			SELECT 'column ' || column_name || ' ' || data_type || ' ' || is_nullable || ' ' || coalesce(column_default, '')
			FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = 'prompts' AND column_name = 'is_public'
			UNION ALL
			SELECT 'index ' || indexdef FROM pg_indexes
			WHERE schemaname = 'public' AND indexname = 'ix_prompts_public'
			ORDER BY 1`)
	}
	if left := sharing(); len(left) != 0 {
		t.Fatalf("prompts can still be shared: %v", left)
	}

	if _, err := pool.Exec(ctx, `
INSERT INTO "user" (id, email) VALUES ('11111111-1111-4111-8111-111111111111', 'a@example.test');
INSERT INTO prompts (user_id, name, prompt, mode) VALUES
 ('11111111-1111-4111-8111-111111111111', 'Shorten', 'Shorten {selection}.', 'transform')`); err != nil {
		t.Fatal(err)
	}

	if _, err := schema.DownTo(ctx, pool, versionBefore(t, promptsUnshared), testKeys); err != nil {
		t.Fatalf("rolling back to %d: %v", versionBefore(t, promptsUnshared), err)
	}
	want := []string{
		"column is_public boolean NO false",
		"index CREATE INDEX ix_prompts_public ON public.prompts USING btree (created_at DESC) WHERE is_public",
	}
	if back := sharing(); !equal(back, want) {
		t.Errorf("the Down brought back\n%v\nwant\n%v", back, want)
	}
	if private := column[bool](t, pool, `SELECT is_public FROM prompts`); len(private) != 1 || private[0] {
		t.Errorf("after the Down the prompt's flag is %v, want one private prompt", private)
	}

	if _, err := schema.Migrate(ctx, pool, testKeys); err != nil {
		t.Fatalf("migrating again: %v", err)
	}
	if left := sharing(); len(left) != 0 {
		t.Errorf("the Up left %v", left)
	}
	if kept := column[string](t, pool, `SELECT name || ' ' || prompt FROM prompts`); !equal(kept, []string{"Shorten Shorten {selection}."}) {
		t.Errorf("the prompt itself changed: %v", kept)
	}
}
