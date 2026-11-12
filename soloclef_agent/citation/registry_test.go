package citation_test

import (
	"sync"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/citation"
)

func TestASourceKeepsItsNumberAndNumbersAreDenseUnderContention(t *testing.T) {
	r := citation.NewRegistry()
	const workers, sources = 16, 50
	got := make([][]int, workers)
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range sources {
				got[w] = append(got[w], r.Number(citation.Source{Kind: "chunk", Ref: string(rune('A' + i))}))
			}
		}()
	}
	wg.Wait()

	// Every worker was told the same number for the same source, and the
	// numbers are exactly 1..50 with none skipped or shared.
	used := map[int]bool{}
	for i := range sources {
		for w := range workers {
			if got[w][i] != got[0][i] {
				t.Fatalf("source %d was numbered %d and %d", i, got[0][i], got[w][i])
			}
		}
		used[got[0][i]] = true
	}
	for n := 1; n <= sources; n++ {
		if !used[n] {
			t.Fatalf("number %d was never given out", n)
		}
	}
	if _, ok := r.Lookup(sources + 1); ok {
		t.Fatal("a number never given out was found")
	}
	if _, ok := r.Lookup(0); ok {
		t.Fatal("zero is not a citation number")
	}
}

func TestARestoredRegistryGoesOnNumberingWhereItStopped(t *testing.T) {
	r := citation.NewRegistry()
	a := citation.Source{Kind: "chunk", Ref: "12"}
	b := citation.Source{Kind: "url", Ref: "https://notes.example/x"}
	r.Number(a)
	early := r.Sources()
	r.Number(b)
	saved := r.Sources()

	restored := citation.NewRegistry()
	if err := restored.Extend(saved); err != nil {
		t.Fatal(err)
	}
	if restored.Number(b) != 2 || restored.Number(a) != 1 {
		t.Fatal("restored sources did not keep their numbers")
	}
	if n := restored.Number(citation.Source{Kind: "run", Ref: "9"}); n != 3 {
		t.Fatalf("next number after restore = %d, want 3", n)
	}

	// An older snapshot of the same sequence is a prefix and changes nothing.
	if err := restored.Extend(early); err != nil || len(restored.Sources()) != 3 {
		t.Fatalf("extending with an older snapshot: %v, %d sources", err, len(restored.Sources()))
	}

	// The snapshot taken earlier was not changed by the numbering after it,
	// and appending to it does not reach into the registry.
	if len(early) != 1 || early[0] != a {
		t.Fatalf("an earlier snapshot changed: %+v", early)
	}
	_ = append(saved, citation.Source{Kind: "chunk", Ref: "intruder"})
	if src, _ := r.Lookup(3); src.Ref == "intruder" {
		t.Fatal("appending to a snapshot wrote into the registry")
	}

	// A list that disagrees on a shared number is refused whole.
	other := citation.NewRegistry()
	other.Number(b)
	if err := other.Extend(saved); err == nil {
		t.Fatal("a saved list that renumbers a source was accepted")
	}
	if len(other.Sources()) != 1 {
		t.Fatal("a refused list changed the registry")
	}
	if err := citation.NewRegistry().Extend([]citation.Source{a, a}); err == nil {
		t.Fatal("a saved list giving one source two numbers was accepted")
	}
}
