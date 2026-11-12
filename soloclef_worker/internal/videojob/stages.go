package videojob

import (
	"sync"
	"time"
)

// stages measures how long each part of one video took: authoring,
// narration, the stills and their layout check, the render, verification, the save. They
// are logged together when the video is done, so a slow video says where
// its time went, and the end-to-end test reads them as its measurement.
type stages struct {
	mu    sync.Mutex
	order []string
	took  map[string]time.Duration
}

func newStages() *stages { return &stages{took: map[string]time.Duration{}} }

// start begins timing a stage and returns the function that ends it. A
// stage run more than once (a render after a repair) adds up.
func (s *stages) start(name string) (stop func()) {
	began := time.Now()
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, seen := s.took[name]; !seen {
			s.order = append(s.order, name)
		}
		s.took[name] += time.Since(began)
	}
}

// Stage is one measured part.
type Stage struct {
	Name string
	Took time.Duration
}

// List is every stage measured, in the order each first ran.
func (s *stages) List() []Stage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Stage, 0, len(s.order))
	for _, name := range s.order {
		out = append(out, Stage{Name: name, Took: s.took[name]})
	}
	return out
}

// logAttrs is the stages as log attributes.
func (s *stages) logAttrs() []any {
	var attrs []any
	for _, stage := range s.List() {
		attrs = append(attrs, "took_"+stage.Name, stage.Took.Round(time.Millisecond).String())
	}
	return attrs
}
