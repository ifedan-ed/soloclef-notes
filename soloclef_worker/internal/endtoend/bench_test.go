package endtoend

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/indexing"
)

// These benchmarks index whole documents against the real services and
// report, beside the usual ns/op, where the time went:
//
//	docs/s        documents made searchable a second
//	chunks/doc    how many chunks a document became
//	<phase>-ms    the mean time a document spent in each phase
//
// Embeddings come from the stub unless LITELLM_URL and LITELLM_KEY_FILE are
// set, so by default the embed phase is the cost of the requests themselves
// and not of a model. Run with:
//
//	bash scripts/index_end_to_end_test.sh bench

// article is about sixty kilobytes of prose and tables, different for every
// number so that no two documents share a chunk.
func article(number int) []byte {
	var text strings.Builder
	fmt.Fprintf(&text, "# Report %d\n\n", number)
	for section := range 40 {
		fmt.Fprintf(&text, "## Section %d of report %d\n\n", section, number)
		for sentence := range 12 {
			fmt.Fprintf(&text, "Finding %d.%d.%d describes what was measured on the ward that week and why it mattered to the team. ", number, section, sentence)
		}
		text.WriteString("\n\n| Measure | Value | Unit |\n| --- | --- | --- |\n")
		for row := range 8 {
			fmt.Fprintf(&text, "| Measure %d.%d.%d | %d | mmol/L |\n", number, section, row, section*row+number)
		}
		text.WriteString("\n")
	}
	return []byte(text.String())
}

// booklet is a twenty-page PDF with forty lines a page.
func booklet(number int) []byte {
	pages := make([][]string, 20)
	for page := range pages {
		lines := make([]string, 40)
		for line := range lines {
			lines[line] = fmt.Sprintf("Booklet %d page %d line %d records one observation made on the ward.", number, page+1, line+1)
		}
		pages[page] = lines
	}
	return pdf(pages...)
}

// report adds the per-document figures to a benchmark's output.
func report(b *testing.B, elapsed time.Duration, documents int, chunks, embedded int64, phases map[string]time.Duration) {
	b.Helper()
	if documents == 0 {
		return
	}
	b.ReportMetric(float64(documents)/elapsed.Seconds(), "docs/s")
	b.ReportMetric(float64(chunks)/float64(documents), "chunks/doc")
	b.ReportMetric(float64(embedded)/float64(documents), "embedded/doc")
	for phase, spent := range phases {
		b.ReportMetric(float64(spent.Microseconds())/1000/float64(documents), phase+"-ms")
	}
}

func benchmarkDocuments(b *testing.B, name string, content func(int) []byte) {
	s := open(b, useRealProxyForBenchmarks(b))
	// Stored before the clock starts: the upload is the route's time.
	ids := make([]int64, b.N)
	var bytes int64
	for index := range ids {
		file := content(index)
		bytes += int64(len(file))
		ids[index] = s.upload(b, s.userID, name, file, "")
	}
	b.SetBytes(bytes / int64(b.N))
	jobs := indexing.NewStatistics()
	b.ResetTimer()
	started := time.Now()
	for _, id := range ids {
		jobs.Observe(s.index(b, id))
	}
	elapsed := time.Since(started)
	b.StopTimer()
	totals := jobs.Snapshot()
	report(b, elapsed, b.N, totals.Chunks, totals.Embedded, totals.Phases)
}

func useRealProxyForBenchmarks(tb testing.TB) bool {
	_, _, named := realProxy(tb)
	return named
}

// BenchmarkIndexMarkdown: a 60 KB Markdown file, read in Go.
func BenchmarkIndexMarkdown(b *testing.B) {
	benchmarkDocuments(b, "report.md", article)
}

// BenchmarkIndexPDF: a 20-page PDF, through the parser service.
func BenchmarkIndexPDF(b *testing.B) {
	benchmarkDocuments(b, "booklet.pdf", booklet)
}

// BenchmarkReindexUnchanged: the same document indexed again, which is what
// a redelivered message costs. Nothing is embedded.
func BenchmarkReindexUnchanged(b *testing.B) {
	s := open(b, useRealProxyForBenchmarks(b))
	id := s.upload(b, s.userID, "report.md", article(0), "")
	s.index(b, id)
	jobs := indexing.NewStatistics()
	b.ResetTimer()
	started := time.Now()
	for range b.N {
		jobs.Observe(s.index(b, id))
	}
	elapsed := time.Since(started)
	b.StopTimer()
	totals := jobs.Snapshot()
	report(b, elapsed, b.N, totals.Chunks, totals.Embedded, totals.Phases)
}

// BenchmarkIndexMarkdownFourAtOnce: four documents in flight, which is the
// worker with a queue concurrency of four. docs/s is the worker's
// throughput; the phase times are per document and so overlap.
func BenchmarkIndexMarkdownFourAtOnce(b *testing.B) {
	s := open(b, useRealProxyForBenchmarks(b))
	ids := make([]int64, b.N)
	for index := range ids {
		ids[index] = s.upload(b, s.userID, "report.md", article(index), "")
	}
	jobs := indexing.NewStatistics()
	var next atomic.Int64
	var failed atomic.Value
	b.ResetTimer()
	started := time.Now()
	var workers sync.WaitGroup
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				index := int(next.Add(1)) - 1
				if index >= len(ids) {
					return
				}
				result, err := s.indexer.Index(context.Background(), ids[index])
				if err != nil || result.Outcome != indexing.Indexed {
					failed.Store(fmt.Sprintf("document %d: outcome %s (%s), err %v", ids[index], result.Outcome, result.Reason, err))
					return
				}
				jobs.Observe(result)
			}
		}()
	}
	workers.Wait()
	elapsed := time.Since(started)
	b.StopTimer()
	if problem := failed.Load(); problem != nil {
		b.Fatal(problem)
	}
	totals := jobs.Snapshot()
	report(b, elapsed, b.N, totals.Chunks, totals.Embedded, totals.Phases)
}
