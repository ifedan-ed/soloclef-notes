// Package citation numbers the sources a turn cites, from one sequence.
//
// The numbers are what the model writes in its answer ("[3]"), so a source
// must keep the number it was first given for as long as text citing it
// exists. The way that goes wrong is two registries: a nested agent numbers
// its own sources from 1, writes prose citing them, and on return its
// registry is merged into the parent's and renumbered, while the prose keeps
// the old numbers and now points at someone else's sources.
//
// The fix is to have nothing to merge. A thread has one Registry. The agent
// loop hands it to every tool call, of the main agent and of every delegate
// beneath it (tool.Call.Citations), so every source, wherever it is found,
// draws its number from one sequence. The loop saves it with the thread and
// restores it on resume, so numbering carries on across a pause, a restart
// and later turns of the same conversation.
package citation

import (
	"fmt"
	"maps"
	"sync"
)

// Source identifies something that can be cited.
type Source struct {
	// Kind is the caller's word for what this is: "chunk", "url", "run".
	Kind string `json:"kind"`

	// Ref locates it within its kind: a chunk id, an address.
	Ref string `json:"ref"`
}

// Registry hands out citation numbers. It is safe for use by many goroutines,
// which is the whole reason it exists: tools of one batch, and delegates side
// by side, register sources at the same time.
type Registry struct {
	mu      sync.Mutex
	numbers map[Source]int
	sources []Source
}

// NewRegistry returns an empty registry whose first number will be 1.
func NewRegistry() *Registry {
	return &Registry{numbers: make(map[Source]int)}
}

// Number returns the source's number, giving it the next one if it has none.
// The same source always gets the same number, so a passage found by two
// searches is cited once.
func (r *Registry) Number(s Source) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n, ok := r.numbers[s]; ok {
		return n
	}
	r.sources = append(r.sources, s)
	n := len(r.sources)
	r.numbers[s] = n
	return n
}

// Lookup returns the source a number was given to. It reports false for a
// number never given out, which is how an answer's invented "[97]" is told
// from a real citation.
func (r *Registry) Lookup(n int) (Source, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n < 1 || n > len(r.sources) {
		return Source{}, false
	}
	return r.sources[n-1], true
}

// Sources returns every source in number order: the source numbered n is at
// index n-1. That list is the whole registry, and it is what a thread's saved
// state keeps.
//
// It does not copy. Sources are only ever appended, so the elements below the
// length read here are never written again, even if a later append moves the
// list to a larger array; the slice is capped at that length so that an
// append by the caller cannot write into the registry's spare capacity
// either. Taking it on every saved step therefore costs nothing.
func (r *Registry) Sources() []Source {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sources[:len(r.sources):len(r.sources)]
}

// Extend brings the registry up to a saved list of sources, keeping every
// number the registry has already given out.
//
// The saved list and the registry must agree on every number they share: one
// of them is a prefix of the other. They always do when both came from one
// sequence, because numbers are only ever appended. That is what lets a
// thread restore its registry from any of the snapshots its runs saved, the
// newest or an older one, in any order: the longer list wins and nothing is
// renumbered. A list that disagrees is refused whole and changes nothing,
// because accepting it would give one number to two sources, which is the
// defect this package exists to prevent.
func (r *Registry) Extend(saved []Source) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	shared := min(len(saved), len(r.sources))
	for i := 0; i < shared; i++ {
		if saved[i] != r.sources[i] {
			return fmt.Errorf("citation: number %d is %s %q here and %s %q in the saved list", i+1, r.sources[i].Kind, r.sources[i].Ref, saved[i].Kind, saved[i].Ref)
		}
	}
	// Checked in full before anything is added, so a refusal changes nothing.
	fresh := saved[shared:]
	if len(fresh) == 0 {
		return nil
	}
	added := make(map[Source]int, len(fresh))
	for i, s := range fresh {
		_, known := r.numbers[s]
		if _, twice := added[s]; known || twice {
			return fmt.Errorf("citation: the saved list gives %s %q a second number, %d", s.Kind, s.Ref, shared+i+1)
		}
		added[s] = shared + i + 1
	}
	maps.Copy(r.numbers, added)
	r.sources = append(r.sources, fresh...)
	return nil
}
