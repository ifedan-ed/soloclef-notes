// Package studiofailures is the sentence a person reads for a Studio output
// that failed, by the failure code its run was closed with.
//
// It is one table for two readers: the runner, which writes the sentence
// into the failure's notification (internal/studiogenerate), and the
// output list, which shows it on the failed row (internal/studiooutputs).
// Before, the sentence reached only the notification and the row said just
// "Failed". Kept apart from both so neither imports the other for it.
package studiofailures

import "git.danvics.com/danvics/soloclef/soloclef_assistant/deliverables"

// The codes an artifact job is closed with by its runner. A run that made
// nothing is closed with the code its specialist's tools noted instead,
// when there is one (deliverables.Failures).
const (
	ModelUnavailable = "model_unavailable"
	Timeout          = "timeout"
	StepLimit        = "step_limit"
	GenerationFailed = "generation_failed"
	NothingMade      = "nothing_made"
	Interrupted      = "interrupted"
	Cancelled        = "cancelled"
)

// artifactSentences is what the owner is told about an artifact job's
// failure, by its code. A code with no sentence says only that the output
// could not be made, which is all the person can act on; the detail is for
// the log. The timeout and step limit end with what the person can do:
// such a run often succeeds on the next try (a report that hit the step
// limit on 2026-11-15 was made at once on retry). generation_failed is
// the runner's code for a run that broke off on an error; nearly always the
// model service's stream breaking part way ("the stream broke: unexpected
// EOF"), which is gone on the next try, so the sentence says that and not
// the rarer causes the log keeps.
var artifactSentences = map[string]string{
	ModelUnavailable: "The model could not be reached. Try again in a few minutes.",
	GenerationFailed: "The connection to the model broke off while it was being made. Try again; it usually works.",
	Timeout:          "It took longer than this server allows. Try again.",
	StepLimit:        "It needed more steps than one output is allowed. Try again.",
	NothingMade:      "The run ended without making anything. Try again.",
	Interrupted:      "The server restarted while it was being made. Try again.",
}

// videoSentences is the same for a video's render job, whose codes the
// worker writes (soloclef_worker/internal/videojob/failure.go). The phone
// and the chat's video card read the code alone; these are the row's.
var videoSentences = map[string]string{
	"duration_limit":      "The video would be too long to render. Ask for a shorter one and try again.",
	"quota_exceeded":      "There is no capacity left to make videos at the moment. Try again later.",
	"generation_failed":   "The video could not be written. Try again.",
	"render_failed":       "The video could not be rendered. Try again.",
	"verification_failed": "The rendered video did not pass its checks. Try again.",
	Interrupted:           "The server restarted while it was being made. Try again.",
}

// ForArtifact is the sentence for an artifact job's code: the runner's own
// first, then the specialist's. "" for a code with neither.
func ForArtifact(code string) string {
	if sentence, known := artifactSentences[code]; known {
		return sentence
	}
	sentence, _ := deliverables.FailureSentence(code)
	return sentence
}

// ForVideo is the sentence for a video job's code, "" for one it does not
// know.
func ForVideo(code string) string {
	return videoSentences[code]
}
