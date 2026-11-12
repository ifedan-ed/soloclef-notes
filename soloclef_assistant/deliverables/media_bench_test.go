package deliverables

import (
	"context"
	"image/color"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
)

// What an infographic and a video cost this process, beside the models and
// the queue they wait for: reading the infographicPlan, preparing the image, and the
// tool's own work around a stubbed image model.

// landscape is an image the size the image model returns for a landscape
// page.
var landscape = picture(1536, 1024, color.NRGBA{R: 240, G: 240, B: 235, A: 255})

func BenchmarkPlannedItems(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if items := plannedItems(infographicPlan, maxCheckedItems); len(items) < 8 {
			b.Fatal(items)
		}
	}
}

// Decoding a 1536x1024 image, flattening it onto white and encoding it
// again: the whole of what normalising costs.
func BenchmarkNormalisedPNG(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(landscape)))
	for b.Loop() {
		if _, _, _, err := normalisedPNG(landscape); err != nil {
			b.Fatal(err)
		}
	}
}

// The JPEG copy sent to the model that reads the image back.
func BenchmarkReadingCopy(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := readingCopy(landscape); err != nil {
			b.Fatal(err)
		}
	}
}

// One infographic from the answered question to the saved artifact, with
// the image model and the reader answering at once: this process's share
// of the minutes an infographic takes.
func BenchmarkInfographicAfterTheAnswer(b *testing.B) {
	l := &ledger{}
	l.painter.images = nil
	s, _ := specialist(b, modeltest.NewScript(), l)
	save := s.saveTool(Owner{ThreadID: 3, UserID: "u-1"})
	call := toolCall(infographicArgs(b, "Photosynthesis", infographicPlan))
	call.Answer = styleAnswer("auto")
	b.ReportAllocs()
	for b.Loop() {
		if _, err := save.Run(context.Background(), call); err != nil {
			b.Fatal(err)
		}
	}
}

// Queueing a video: the checks and the request's encoding.
func BenchmarkQueueVideo(b *testing.B) {
	videos := Videos{
		Rows: fixedRows{JobID: 1, TaskID: videoTaskID(1), Created: true}, Logger: discardLogger(),
		Publish: func(context.Context, int64, string) error { return nil },
	}
	material := ""
	for len(material) < 60_000 {
		material += "Mitosis has four phases, and each has its own checkpoint.\n"
	}
	owner := Owner{ThreadID: 3, UserID: "u-1"}
	b.ReportAllocs()
	for b.Loop() {
		request, err := newVideoRequest("Mitosis explained", "A five minute explainer.", material)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := videos.Queue(context.Background(), owner, "call-1", request); err != nil {
			b.Fatal(err)
		}
	}
}

// fixedRows is VideoRows that answers one job and keeps nothing, so that a
// benchmark's memory is the code's and not the stand-in's.
type fixedRows QueuedVideo

func (r fixedRows) CreateVideo(context.Context, Owner, string, string, []byte) (QueuedVideo, error) {
	return QueuedVideo(r), nil
}
