package main

import (
	"context"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/indexing"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/metrics"
)

// indexMetrics serves what the indexer knows about its own work: how jobs
// ended, and where their time went. A provider outage is then a line on a
// graph (retries and failures rising, the embedding phase's seconds
// climbing) and not something learned from a user.
func indexMetrics(jobs *indexing.Statistics) metrics.Collector { return jobCollector{jobs} }

type jobCollector struct{ jobs *indexing.Statistics }

func (c jobCollector) Collect(_ context.Context, into *metrics.Writer) error {
	snapshot := c.jobs.Snapshot()

	into.Declare("index_documents_total", metrics.Counter, "Indexing jobs finished, by how they ended.")
	for outcome, count := range snapshot.Outcomes {
		into.Sample("index_documents_total", float64(count), "outcome", string(outcome))
	}
	into.Declare("index_chunks_total", metrics.Counter, "Chunks written by indexed documents.")
	into.Sample("index_chunks_total", float64(snapshot.Chunks))
	into.Declare("index_chunks_embedded_total", metrics.Counter, "Chunks sent to the embedding model; the rest kept their vectors.")
	into.Sample("index_chunks_embedded_total", float64(snapshot.Embedded))
	into.Declare("index_bytes_total", metrics.Counter, "Bytes of files fetched from storage for indexing.")
	into.Sample("index_bytes_total", float64(snapshot.Bytes))
	into.Declare("index_phase_seconds_total", metrics.Counter, "Time indexed documents spent in each phase.")
	for phase, spent := range snapshot.Phases {
		into.Sample("index_phase_seconds_total", spent.Seconds(), "phase", phase)
	}
	return nil
}
