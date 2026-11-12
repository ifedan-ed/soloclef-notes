package graph_test

import (
	"context"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/graph"
)

// BenchmarkGraphStep is the overhead of one node: a ten-node loop over a
// small state, each step saved to an in-memory store.
func BenchmarkGraphStep(b *testing.B) {
	type counter struct {
		N int `json:"n"`
	}
	g, err := graph.New[counter]().
		Node("tick", func(_ context.Context, _ graph.Step, c counter) (counter, error) { c.N++; return c, nil }).
		Start("tick").
		Branch("tick", func(_ context.Context, c counter) (graph.Route, error) {
			if c.N%10 == 0 {
				return graph.To(graph.End), nil
			}
			return graph.To("tick"), nil
		}, "tick", graph.End).
		Build()
	if err != nil {
		b.Fatal(err)
	}
	run := graph.Run{Thread: "t", MaxSteps: 100, Store: checkpoint.NewMemory(), Sink: event.Discard}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.Start(ctx, run, counter{}); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*10), "ns/step")
}
