package uistream_test

import (
	"io"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/uistream"
)

// BenchmarkTextDeltaFrame is the cost of writing one streamed token as a
// frame, which a chat does thousands of times per answer.
func BenchmarkTextDeltaFrame(b *testing.B) {
	now := time.Unix(0, 0)
	w, err := uistream.New(uistream.Config{
		Out: io.Discard, Flush: func() error { return nil }, Now: func() time.Time { return now },
		NewID: uistream.RandomID, Card: uistream.LengthOnly,
		Approval: func(event.Event) uistream.ApprovalCard { return uistream.ApprovalCard{} },
	})
	if err != nil {
		b.Fatal(err)
	}
	ev := event.Event{Kind: event.TextDelta, Text: "token "}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.Event(ev)
	}
}
