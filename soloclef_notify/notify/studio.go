package notify

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"git.danvics.com/danvics/soloclef/soloclef_notify/pushpolicy"
)

// StudioRun is a Studio output's run that has ended: a podcast run (an
// audio overview) or a deliverable job (an artifact the Studio or the chat
// made, or a video). Exactly one of PodcastID and JobID is set.
type StudioRun struct {
	UserID    string
	PodcastID int64
	JobID     int64
	// Failed is false for a run that is ready. A cancelled run is not
	// reported at all: the person stopped it.
	Failed bool
	// Reason is what the person is told about a failure, in the words the
	// run's own row gives them, or "".
	Reason string
}

// The inbox type of a Studio output's row.
const studioType = "studio_output"

// StudioFinished notifies the owner that an output is ready or could not be
// made. The output, its notebook and their names are read from the run's
// rows, naming the owner, so a caller passes ids and nothing it would have
// to keep in step.
func (n *Notifier) StudioFinished(ctx context.Context, run StudioRun) error {
	if run.UserID == "" || (run.PodcastID == 0) == (run.JobID == 0) {
		return errors.New("notify: a Studio run names its owner and exactly one of a podcast and a job")
	}
	q := New(n.Database)
	var found studioOutput
	if run.PodcastID != 0 {
		row, err := q.PodcastOutput(ctx, PodcastOutputParams{PodcastID: run.PodcastID, UserID: run.UserID})
		if err != nil {
			return fmt.Errorf("notify: reading podcast %d: %w", run.PodcastID, err)
		}
		found = studioOutput{runTitle: row.RunTitle, runKind: "audio", threadID: row.ThreadID, outputID: row.OutputID,
			outputKind: row.OutputKind, outputTitle: row.OutputTitle, notebookID: row.NotebookID, notebookTitle: row.NotebookTitle}
	} else {
		row, err := q.DeliverableOutput(ctx, DeliverableOutputParams{JobID: run.JobID, UserID: run.UserID})
		if err != nil {
			return fmt.Errorf("notify: reading job %d: %w", run.JobID, err)
		}
		found = studioOutput{runTitle: row.RunTitle, runKind: row.RunKind, threadID: row.ThreadID, outputID: row.OutputID,
			outputKind: row.OutputKind, outputTitle: row.OutputTitle, notebookID: row.NotebookID, notebookTitle: row.NotebookTitle}
	}
	_, err := n.record(ctx, studioEvent(run, found))
	return err
}

// studioOutput is what the rows say about a run's output.
type studioOutput struct {
	runTitle, runKind                      string
	threadID, outputID, notebookID         *int64
	outputKind, outputTitle, notebookTitle *string
}

// kindLabels is how each output kind is named in a title. A kind not here
// (a chat's artifact, whose format the job does not record) is "Output".
var kindLabels = map[string]string{
	"audio":       "Audio overview",
	"video":       "Video",
	"slides":      "Slide deck",
	"report":      "Report",
	"quiz":        "Quiz",
	"flashcards":  "Flashcards",
	"mindmap":     "Mind map",
	"infographic": "Infographic",
	"data_table":  "Data table",
}

// studioEvent is the row and the push for an ended run.
func studioEvent(run StudioRun, output studioOutput) event {
	kind := output.runKind
	if output.outputKind != nil {
		kind = *output.outputKind
	}
	label, known := kindLabels[kind]
	if !known {
		kind, label = "artifact", "Output"
	}
	title := output.runTitle
	if output.outputTitle != nil && *output.outputTitle != "" {
		title = *output.outputTitle
	}
	notebookTitle := ""
	if output.notebookTitle != nil {
		notebookTitle = *output.notebookTitle
	}

	where := ""
	if notebookTitle != "" {
		where = " in " + notebookTitle
	}
	status, headline, message := "completed", label+" ready", fmt.Sprintf("“%s” is ready%s.", title, where)
	if run.Failed {
		status, headline = "failed", label+" could not be made"
		message = fmt.Sprintf("“%s” could not be made%s.", title, where)
		if run.Reason != "" {
			message += " " + run.Reason
		}
	}

	// The phone opens the notebook's Studio, where the output and its Try
	// again are; an output with no notebook (a plain chat's) opens the
	// inbox, which is the one screen sure to show it.
	path := "/inbox"
	if output.notebookID != nil {
		path = "/notebook/" + strconv.FormatInt(*output.notebookID, 10) + "?pane=studio"
	}
	return event{
		userID: run.UserID, kind: pushpolicy.Output, typ: studioType,
		title: headline, message: message,
		push: push{Kind: pushpolicy.Output, Title: headline, Body: notebookTitle, Path: path},
		metadata: map[string]any{
			"status": status, "output_kind": kind, "output_title": title,
			"output_id": output.outputID, "notebook_id": output.notebookID, "notebook_title": output.notebookTitle,
			"thread_id": output.threadID, "retry": run.Failed, "error_message": nonEmpty(run.Reason),
		},
	}
}

// nonEmpty is nil for "", so the row's JSON says null rather than "".
func nonEmpty(text string) *string {
	if text == "" {
		return nil
	}
	return &text
}
