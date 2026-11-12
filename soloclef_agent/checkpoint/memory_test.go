package checkpoint_test

import (
	"context"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint/checkpointtest"
)

func TestMemoryKeepsTheStoreContract(t *testing.T) {
	checkpointtest.Run(t, checkpoint.NewMemory(), "")
}

func TestMemoryDoesNotShareItsBytesWithCallers(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemory()
	state := []byte("original")
	if err := store.Save(ctx, checkpoint.Checkpoint{Thread: "t", Seq: 1, Status: checkpoint.Done, State: state}); err != nil {
		t.Fatal(err)
	}
	// Neither the slice that was saved nor a slice that was loaded is the
	// store's own: writing to either must not change what is stored.
	state[0] = 'X'
	loaded, _ := store.Load(ctx, "t")
	loaded.State[1] = 'X'
	again, _ := store.Load(ctx, "t")
	if string(again.State) != "original" {
		t.Fatalf("stored state was changed from outside: %q", again.State)
	}
}
