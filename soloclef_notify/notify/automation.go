package notify

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_notify/pushpolicy"
)

// AutomationRun is one automation run that ended. Status is the run's
// automation_run_status: succeeded, failed, needs_attention or timed_out. A
// cancelled run is not reported: the person cancelled it.
type AutomationRun struct {
	UserID       string
	AutomationID int64
	RunID        int64
	Name         string
	Status       string
}

// An automation that runs often (every few minutes) would otherwise push
// every few minutes: its results of one outcome within ten minutes are one
// row, and only the first of them pushes. The first push is not held back,
// because one run is the usual case and its result is wanted now.
const automationWindow = 10 * time.Minute

const automationType = "automation_run"

// automationWords is each ending's inbox verb and push title.
var automationWords = map[string]struct{ verb, push, status string }{
	"succeeded":       {"finished", "Automation finished", "completed"},
	"failed":          {"failed", "Automation failed", "failed"},
	"needs_attention": {"needs your attention", "Automation needs your attention", "failed"},
	"timed_out":       {"timed out", "Automation timed out", "failed"},
}

// AutomationFinished notifies the automation's owner that a run ended. It
// is sent beside the email a run may also send, never instead of it.
func (n *Notifier) AutomationFinished(ctx context.Context, run AutomationRun) error {
	words, known := automationWords[run.Status]
	switch {
	case run.Status == "cancelled":
		return nil
	case !known:
		return fmt.Errorf("notify: %q is not how an automation run ends", run.Status)
	case run.UserID == "" || run.AutomationID == 0 || run.RunID == 0:
		return errors.New("notify: an automation run names its owner, its automation and itself")
	}
	name := clipTitle(run.Name)
	render := func(count int, _ []string) rendered {
		if count == 1 {
			return rendered{title: fmt.Sprintf("“%s” %s", name, words.verb), message: automationMessage(run.Status), pushTitle: words.push}
		}
		return rendered{
			title:     fmt.Sprintf("“%s” %s (%d runs)", name, words.verb, count),
			message:   automationMessage(run.Status) + " The newest run is linked.",
			pushTitle: words.push,
		}
	}
	first := render(1, nil)
	path := "/automation/" + strconv.FormatInt(run.AutomationID, 10) + "/runs/" + strconv.FormatInt(run.RunID, 10)
	_, err := n.record(ctx, event{
		userID: run.UserID, kind: pushpolicy.Automation, typ: automationType,
		title: first.title, message: first.message,
		// The automation's name is the person's own words: the push says
		// only what happened, and the path opens the run.
		push: push{Kind: pushpolicy.Automation, Title: words.push, Path: path},
		metadata: map[string]any{
			"status": words.status, "run_status": run.Status,
			"automation_id": run.AutomationID, "automation_name": run.Name, "run_id": run.RunID,
		},
		group: &group{
			key:    "automation:" + strconv.FormatInt(run.AutomationID, 10) + ":" + run.Status,
			window: automationWindow, item: name, render: render,
		},
	})
	return err
}

func automationMessage(status string) string {
	switch status {
	case "succeeded":
		return "The run finished. Open it to see what it did."
	case "needs_attention":
		return "The run stopped at a step that needs you. Open it to see which."
	case "timed_out":
		return "The run took longer than it is allowed and was stopped."
	}
	return "The run could not finish. Open it to see which step failed."
}
