package publiclinks

import (
	"context"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres for this package's database
// test. **Every account in it is deleted** (internal/testpostgres), so it
// must never name a database anyone uses. With it unset the test is skipped.
const databaseVariable = "SOLOCLEF_API_TEST_PUBLICLINKS_DATABASE"

// TestTheStatementsOverARealDatabase asks the two statements what the route
// tests ask a stand-in: that the list is its owner's alone and in its order,
// that it reads a title out of whatever the copy holds without failing, and
// that the revoke removes the owner's link and nobody else's.
func TestTheStatementsOverARealDatabase(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	ctx := context.Background()
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	// Alice has shared two chats and one studio output; two of the links
	// were made in the same instant. Bob has shared one chat. The copies
	// hold every kind of title: text, none, a number, and a copy that is
	// not an object at all.
	testpostgres.Exec(t, pool, `
INSERT INTO new_chat_threads (id, user_id, title) VALUES
 (300, '`+alice+`', 'A chat of hers'), (301, '`+alice+`', ''), (302, '`+bob+`', 'A chat of his');
INSERT INTO notebooks (id, user_id, title) VALUES (7, '`+alice+`', 'A notebook');
INSERT INTO notebook_outputs (id, notebook_id, user_id, kind, title) VALUES
 (40, 7, '`+alice+`', 'flashcards', 'A deck');
INSERT INTO public_chat_snapshots (id, user_id, thread_id, notebook_output_id, share_token,
                                   content_hash, snapshot_data, message_ids, expires_at, created_at) VALUES
 (500, '`+alice+`', 300,  NULL, 'tok-500', 'h-500', '{"title": "Frozen title"}', '{1,2,3}', NULL, '2026-03-01 00:00:05+00'),
 (501, '`+alice+`', 301,  NULL, 'tok-501', 'h-501', '{"title": 7}',              '{4}',     '2026-01-01 00:00:00+00', '2026-03-01 00:00:02+00'),
 (502, '`+alice+`', NULL, 40,   'tok-502', 'h-502', '["not", "an", "object"]',   '{}',      NULL, '2026-03-01 00:00:02+00'),
 (503, '`+bob+`',   302,  NULL, 'tok-503', 'h-503', '{}',                        '{9}',     NULL, '2026-03-01 00:00:09+00');`)
	store := Store{Pool: pool}

	t.Run("the list is its owner's, newest first, by id within a tie", func(t *testing.T) {
		links, err := store.Links(ctx, alice)
		if err != nil || len(links) != 3 {
			t.Fatalf("%d links, %v", len(links), err)
		}
		if links[0].ID != 500 || links[1].ID != 502 || links[2].ID != 501 {
			t.Errorf("listed in the order %d, %d, %d", links[0].ID, links[1].ID, links[2].ID)
		}
		if links[0].Title() != "Frozen title" || links[0].Messages != 3 || links[0].OwnerID != alice ||
			links[0].ThreadID == nil || *links[0].ThreadID != 300 || links[0].NotebookID != nil {
			t.Errorf("the shared chat reads %+v", links[0])
		}
		// A copy that is not an object, for an output: no title of its own
		// and no thread, and the notebook the output belongs to.
		if links[1].Title() != "Untitled" || links[1].NotebookID == nil || *links[1].NotebookID != 7 || links[1].Messages != 0 {
			t.Errorf("the shared output reads %+v", links[1])
		}
		// A title that is a number is no title, and the chat's own is empty.
		if links[2].Title() != "Untitled" || links[2].ExpiresAt == nil {
			t.Errorf("the lapsed link reads %+v", links[2])
		}
		his, err := store.Links(ctx, bob)
		if err != nil || len(his) != 1 || his[0].ID != 503 || his[0].Title() != "A chat of his" {
			t.Errorf("the other account lists %+v, %v", his, err)
		}
	})

	t.Run("a revoke removes the owner's link and nobody else's", func(t *testing.T) {
		count := func() (left int) {
			if err := pool.QueryRow(ctx, "SELECT count(*) FROM public_chat_snapshots").Scan(&left); err != nil {
				t.Fatal(err)
			}
			return left
		}
		// Bob, about Alice's link; Alice, about Bob's; a link nobody has.
		for _, attempt := range []struct {
			who string
			id  int64
		}{{bob, 500}, {alice, 503}, {alice, 999}} {
			if removed, err := store.Revoke(ctx, attempt.who, attempt.id); err != nil || removed || count() != 4 {
				t.Errorf("%s revoking %d: removed %v, %v, %d links left", attempt.who, attempt.id, removed, err, count())
			}
		}
		if removed, err := store.Revoke(ctx, alice, 500); err != nil || !removed || count() != 3 {
			t.Errorf("the owner's revoke: removed %v, %v, %d links left", removed, err, count())
		}
		// Twice is once: the second finds nothing.
		if removed, err := store.Revoke(ctx, alice, 500); err != nil || removed {
			t.Errorf("a second revoke: removed %v, %v", removed, err)
		}
	})
}
