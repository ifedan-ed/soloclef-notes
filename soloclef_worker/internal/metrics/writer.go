// Package metrics serves what the worker tier already knows about itself.
//
// Consumer.Snapshot has had queue depth, in-flight counts and true queue wait
// since phase 2, and nothing read them: `celery_heavy` could report none of it
// and neither could this worker, which left the one number that made the heavy
// queue's ceiling visible — a queue with work on it and nobody taking it —
// behind a debugger.
//
// The text format is written by hand rather than through a client library:
// what is exposed is a handful of gauges and counters read from a snapshot,
// and a dependency that owns a global registry would be more machinery than
// the thing it exposes.
package metrics

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Prefix is on every metric this worker exposes, so one dashboard selector
// finds the tier.
const Prefix = "soloclef_worker_"

// Kind is the Prometheus metric type. Only these two are emitted: a histogram
// would need buckets chosen before anything is known about the distribution,
// and the wait is exposed as a sum and a count instead, which is what a mean
// needs.
type Kind string

const (
	Gauge   Kind = "gauge"
	Counter Kind = "counter"
)

// Writer builds one scrape. A metric is declared once, with its help and its
// type, and then sampled, which is the order the text format requires.
type Writer struct {
	out      strings.Builder
	declared map[string]bool
}

func newWriter() *Writer {
	return &Writer{declared: map[string]bool{}}
}

// Declare writes a metric's HELP and TYPE. Declaring the same name twice is a
// scrape Prometheus rejects, so the second declaration is dropped.
func (w *Writer) Declare(name string, kind Kind, help string) {
	full := Prefix + name
	if w.declared[full] {
		return
	}
	w.declared[full] = true
	fmt.Fprintf(&w.out, "# HELP %s %s\n# TYPE %s %s\n", full, help, full, kind)
}

// Sample writes one value, with label pairs given as name, value, name, value.
func (w *Writer) Sample(name string, value float64, labels ...string) {
	fmt.Fprintf(&w.out, "%s%s%s %s\n", Prefix, name, labelSet(labels), number(value))
}

func (w *Writer) String() string { return w.out.String() }

func labelSet(labels []string) string {
	if len(labels) == 0 {
		return ""
	}
	var out strings.Builder
	out.WriteByte('{')
	for i := 0; i+1 < len(labels); i += 2 {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteString(labels[i])
		out.WriteString(`="`)
		out.WriteString(escapeLabel(labels[i+1]))
		out.WriteString(`"`)
	}
	out.WriteByte('}')
	return out.String()
}

// escapeLabel escapes the three characters the text format gives meaning to.
// A queue or task name is not this worker's to choose, and one with a quote in
// it would otherwise break the whole scrape rather than its own line.
func escapeLabel(value string) string {
	replaced := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return replaced.Replace(value)
}

func number(value float64) string {
	switch {
	case math.IsNaN(value):
		return "NaN"
	case math.IsInf(value, 1):
		return "+Inf"
	case math.IsInf(value, -1):
		return "-Inf"
	default:
		return strconv.FormatFloat(value, 'g', -1, 64)
	}
}
