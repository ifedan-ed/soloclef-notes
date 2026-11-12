// Package checkpointtest is the contract every checkpoint.Store must keep,
// as one test function. The in-memory and Postgres stores both run it, and
// so should any store written later: a store that passes is one the agent
// loop's guarantees hold on.
package checkpointtest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
)

// Run checks store against the contract. prefix is put in front of every
// thread id it uses, so that a shared database can be tested without two
// runs colliding.
func Run(t *testing.T, store checkpoint.Store, prefix string) {
	ctx := context.Background()
	thread := func(name string) string { return prefix + name }

	t.Run("a thread never saved is not found", func(t *testing.T) {
		if _, err := store.Load(ctx, thread("absent")); !errors.Is(err, checkpoint.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("what is saved is what is loaded", func(t *testing.T) {
		// The state holds what a jsonb column would refuse (U+0000) and what
		// a text column would mangle (bytes that are not UTF-8): the store
		// must not look inside it.
		state := []byte("{\"text\":\"nul \\u0000 inside\"}\x00\xff\xfe")
		want := checkpoint.Checkpoint{Thread: thread("roundtrip"), Seq: 1, Status: checkpoint.Paused, Node: "tools", State: state}
		if err := store.Save(ctx, want); err != nil {
			t.Fatal(err)
		}
		got, err := store.Load(ctx, want.Thread)
		if err != nil {
			t.Fatal(err)
		}
		if got.Thread != want.Thread || got.Seq != 1 || got.Status != checkpoint.Paused || got.Node != "tools" || !bytes.Equal(got.State, state) {
			t.Fatalf("loaded %+v, want %+v", got, want)
		}

		// A later save replaces it; an empty state and an empty node are
		// values, not absences.
		next := checkpoint.Checkpoint{Thread: want.Thread, Seq: 2, Status: checkpoint.Done}
		if err := store.Save(ctx, next); err != nil {
			t.Fatal(err)
		}
		got, err = store.Load(ctx, want.Thread)
		if err != nil || got.Seq != 2 || got.Status != checkpoint.Done || got.Node != "" || len(got.State) != 0 {
			t.Fatalf("after second save: %+v, %v", got, err)
		}
	})

	t.Run("a save out of sequence is a conflict and changes nothing", func(t *testing.T) {
		id := thread("sequence")
		first := checkpoint.Checkpoint{Thread: id, Seq: 1, Status: checkpoint.Running, Node: "model", State: []byte("one")}
		// A thread must begin at 1.
		for _, seq := range []int64{0, 2, 7} {
			bad := first
			bad.Seq = seq
			if err := store.Save(ctx, bad); !errors.Is(err, checkpoint.ErrConflict) {
				t.Fatalf("first save with seq %d: err = %v, want ErrConflict", seq, err)
			}
		}
		if err := store.Save(ctx, first); err != nil {
			t.Fatal(err)
		}
		// A repeat, a skip and a step back are all refused.
		for _, seq := range []int64{1, 3, 0} {
			bad := checkpoint.Checkpoint{Thread: id, Seq: seq, Status: checkpoint.Done, State: []byte("intruder")}
			if err := store.Save(ctx, bad); !errors.Is(err, checkpoint.ErrConflict) {
				t.Fatalf("save with seq %d over seq 1: err = %v, want ErrConflict", seq, err)
			}
		}
		got, err := store.Load(ctx, id)
		if err != nil || got.Seq != 1 || string(got.State) != "one" {
			t.Fatalf("a refused save changed the thread: %+v, %v", got, err)
		}
	})

	t.Run("of many writers racing for one step exactly one wins", func(t *testing.T) {
		id := thread("race")
		if err := store.Save(ctx, checkpoint.Checkpoint{Thread: id, Seq: 1, Status: checkpoint.Running}); err != nil {
			t.Fatal(err)
		}
		const writers = 16
		errs := make([]error, writers)
		var wg sync.WaitGroup
		for i := range writers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[i] = store.Save(ctx, checkpoint.Checkpoint{Thread: id, Seq: 2, Status: checkpoint.Running, State: []byte(fmt.Sprint(i))})
			}()
		}
		wg.Wait()
		won := -1
		for i, err := range errs {
			switch {
			case err == nil && won >= 0:
				t.Fatalf("writers %d and %d both saved seq 2", won, i)
			case err == nil:
				won = i
			case !errors.Is(err, checkpoint.ErrConflict):
				t.Fatalf("writer %d: err = %v, want nil or ErrConflict", i, err)
			}
		}
		if won < 0 {
			t.Fatal("no writer saved seq 2")
		}
		// And what is stored is the winner's, whole.
		got, err := store.Load(ctx, id)
		if err != nil || string(got.State) != fmt.Sprint(won) {
			t.Fatalf("stored state %q is not writer %d's (%v)", got.State, won, err)
		}
	})

	t.Run("delete removes a thread and is not an error for one that is absent", func(t *testing.T) {
		id := thread("delete")
		if err := store.Save(ctx, checkpoint.Checkpoint{Thread: id, Seq: 1, Status: checkpoint.Done}); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := store.Delete(ctx, id); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := store.Load(ctx, id); !errors.Is(err, checkpoint.ErrNotFound) {
			t.Fatalf("after delete: err = %v, want ErrNotFound", err)
		}
		// A deleted thread begins again at 1.
		if err := store.Save(ctx, checkpoint.Checkpoint{Thread: id, Seq: 1, Status: checkpoint.Running}); err != nil {
			t.Fatalf("first save after delete: %v", err)
		}
	})

	t.Run("deleting a tree removes the thread and what is nested under it, and nothing else", func(t *testing.T) {
		// The names are chosen to catch a store that matches by prefix or
		// by pattern: "tree-1" begins "tree-10" and "tree-1x", and a name
		// may hold the pattern characters themselves.
		root := thread("tree-1")
		gone := []string{root, root + "/call_a", root + "/call_a/call_b", root + "/"}
		kept := []string{thread("tree-10"), thread("tree-1x"), thread("tree-10/call_a"), thread("tree-"), thread("tree-1%"), thread("tree-_/call_a")}
		for _, id := range append(append([]string(nil), gone...), kept...) {
			if err := store.Save(ctx, checkpoint.Checkpoint{Thread: id, Seq: 1, Status: checkpoint.Paused}); err != nil {
				t.Fatal(err)
			}
		}
		// Twice: the second finds nothing and is not an error.
		for range 2 {
			if err := store.DeleteTree(ctx, root); err != nil {
				t.Fatal(err)
			}
		}
		for _, id := range gone {
			if _, err := store.Load(ctx, id); !errors.Is(err, checkpoint.ErrNotFound) {
				t.Errorf("%q survived: err = %v", id, err)
			}
		}
		for _, id := range kept {
			if _, err := store.Load(ctx, id); err != nil {
				t.Errorf("%q is not under %q and was removed: %v", id, root, err)
			}
		}
		// A name holding pattern characters deletes itself and its own
		// nested threads only.
		if err := store.DeleteTree(ctx, thread("tree-_")); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Load(ctx, thread("tree-_/call_a")); !errors.Is(err, checkpoint.ErrNotFound) {
			t.Errorf("a thread nested under a name with \"_\" survived: %v", err)
		}
		if _, err := store.Load(ctx, thread("tree-1x")); err != nil {
			t.Errorf("\"_\" in a name was read as a pattern: %v", err)
		}
	})

	t.Run("a cancelled context is refused", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if err := store.Save(cancelled, checkpoint.Checkpoint{Thread: thread("cancelled"), Seq: 1, Status: checkpoint.Running}); err == nil {
			t.Fatal("a save on a cancelled context succeeded")
		}
		if _, err := store.Load(cancelled, thread("roundtrip")); err == nil {
			t.Fatal("a load on a cancelled context succeeded")
		}
	})
}
