package podcast

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/audiofile"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/proxystub"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/speech"
)

// The render's own cost, with a provider that answers at once: what the
// worker spends on a line (the request, measuring the answer, making it MP3,
// the cache) and on the episode (the join and the duration). The provider's
// own time is not in it; that is the provider's.
//
//	go test ./internal/podcast/ -run '^$' -bench Render -benchtime 3x
func BenchmarkRenderOfATenMinuteEpisode(b *testing.B) {
	for _, answer := range []struct {
		name  string
		audio func() []byte
	}{
		{"mp3", nil},
		{"wav", func() []byte { return proxystub.Tone(5) }},
	} {
		b.Run(answer.name, func(b *testing.B) {
			tools, err := audiofile.Find()
			if err != nil {
				b.Skip(err)
			}
			mp3, err := tools.ToMP3(context.Background(), proxystub.Tone(5))
			if err != nil {
				b.Fatal(err)
			}
			stub := proxystub.Start()
			defer stub.Close()
			stub.Speech = func(proxystub.SpeechRequest) ([]byte, int) {
				if answer.audio != nil {
					return answer.audio(), http.StatusOK
				}
				return mp3, http.StatusOK
			}
			// 120 lines of five seconds: a ten-minute episode.
			var turns []Turn
			for i := 0; i < 120; i++ {
				turns = append(turns, Turn{Speaker: i % 2, Text: fmt.Sprintf("Line %d of the episode.", i)})
			}
			transcript, _ := json.Marshal(Transcript{Turns: turns})
			service, _ := speech.ReadService("openai/gpt-4o-mini-tts")
			var sink io.Writer = io.Discard
			if os.Getenv("PODCAST_BENCH_LOG") != "" {
				sink = os.Stderr
			}
			log := slog.New(slog.NewTextHandler(sink, nil))
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				rows := &memoryRows{run: Run{ID: 5, UserID: "u", Status: statusRendering, Title: "Bench", Spec: []byte(twoSpeakerBrief), Transcript: transcript}}
				job := &Job{
					Rows: rows, Models: fakeModels{&scriptedModel{}}, Audio: tools, Indexer: &recordingIndexer{},
					Voicer: speech.Client{BaseURL: stub.BaseURL(), Key: "k", Service: service, HTTP: &http.Client{}, Audio: tools,
						AttemptTimeout: 10 * time.Second, MaxAttempts: 1, Log: log},
					Config: Config{CallTimeout: time.Second, Speech: service, Concurrency: 8, WorkDirectory: b.TempDir(), StatusInterval: time.Second},
					Log:    log,
				}
				if err := job.Run(context.Background(), &celery.Message{Task: Task, Args: []any{float64(5)}}); err != nil {
					b.Fatal(err)
				}
				if rows.delivered == nil {
					b.Fatal("nothing delivered")
				}
				b.ReportMetric(rows.delivered.Seconds, "episode-seconds")
			}
		})
	}
}
