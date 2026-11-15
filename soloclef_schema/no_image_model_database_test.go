package schema_test

import (
	"context"
	"testing"

	schema "git.danvics.com/danvics/soloclef/soloclef_schema"
)

// noImageModel is 00026, which removed the image model as an account's
// choice: nothing is drawn by a model any more.
const noImageModel = 26

// The Up removes the stored image choices and refuses new ones, leaving
// every other choice; the Down lets the purpose be stored again.
func TestNoImageModelIsChosen(t *testing.T) {
	pool := migrated(t)
	ctx := context.Background()
	if _, err := schema.DownTo(ctx, pool, versionBefore(t, noImageModel), testKeys); err != nil {
		t.Fatalf("rolling back to %d: %v", versionBefore(t, noImageModel), err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO "user" (id, email) VALUES ('11111111-1111-4111-8111-111111111111', 'a@example.test');
INSERT INTO user_models (user_id, purpose, model) VALUES
 ('11111111-1111-4111-8111-111111111111', 'image', 'some-image-model'),
 ('11111111-1111-4111-8111-111111111111', 'vision', 'some-vision-model')`); err != nil {
		t.Fatal(err)
	}
	if _, err := schema.Migrate(ctx, pool, testKeys); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	if left := column[string](t, pool, `SELECT purpose || '=' || model FROM user_models ORDER BY purpose`); !equal(left, []string{"vision=some-vision-model"}) {
		t.Errorf("after the Up: %v", left)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_models (user_id, purpose, model) VALUES ('11111111-1111-4111-8111-111111111111', 'image', 'x')`); err == nil {
		t.Error("an image model could still be chosen")
	}
}
