package openaicompat

import (
	"bytes"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
)

// BenchmarkReadStream is the cost of taking one streamed token off the wire:
// finding the line, decoding its JSON, handing the text on. It is in the
// package, not beside the public tests, to measure the parser without an
// HTTP round trip around it.
func BenchmarkReadStream(b *testing.B) {
	const tokens = 1000
	var body strings.Builder
	for i := 0; i < tokens; i++ {
		body.WriteString(`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":"token "},"finish_reason":null}]}` + "\n\n")
	}
	body.WriteString(`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n" + "data: [DONE]\n\n")
	raw := []byte(body.String())

	b.SetBytes(int64(len(raw)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := readStream(bytes.NewReader(raw), func(model.Chunk) {}); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*tokens), "ns/token")
}
