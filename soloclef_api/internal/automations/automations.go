// Package automations serves an account's automations: saved agent tasks
// that run on a schedule, when a document arrives in a folder, or when the
// person presses Run now. Thirteen routes under /api/v1/automations: the
// automation itself (list, create, read, update, delete), its triggers
// (add, change, remove), its runs (list, one, cancel), Run now, and whether
// the account's model can run one.
//
// This tier saves and shows; the worker runs (soloclef_worker's
// automationrun). The two agree through soloclef_automations: a definition
// or a trigger is checked here by the code the worker reads it with, and a
// run is created here by the statement the worker's triggers create one
// with.
//
// # Whose an automation is
//
// Its owner's and nobody else's. Every statement names the automation by
// its id and the caller's account together; a trigger or a run is reached
// through its automation in that same statement. Another account's
// automation, trigger or run is the 404 of one that was never made.
//
// # What this does better than the routes it replaces
//
//   - Nothing is saved that cannot run. An action, a condition or a trigger
//     type the worker does not have is refused in a sentence when it is
//     saved, and so is anything a definition names that is not there: a
//     notebook or a connected account that is not the caller's, a model
//     the proxy does not offer, an emailed result on a server that sends
//     no mail (vouch). These used to be saved and fail, or be ignored, at
//     run time.
//   - Run now exists. The page could only wait for a schedule.
//   - A trigger read with another automation's id is not found. The old
//     lookup fetched the trigger by its own id and compared afterwards.
//   - Lists have a total order (newest first, then id).
//   - Cancelling is one statement that names the statuses it may change,
//     so a run that finishes as the button is pressed keeps its ending.
package automations

import "net/http"

// The paths, as the web app calls them.
const (
	ListPath        = "/api/v1/automations"
	EligibilityPath = "/api/v1/automations/model-eligibility"
	OnePath         = "/api/v1/automations/{automation_id}"
	RunNowPath      = "/api/v1/automations/{automation_id}/run"
	TriggersPath    = "/api/v1/automations/{automation_id}/triggers"
	TriggerPath     = "/api/v1/automations/{automation_id}/triggers/{trigger_id}"
	RunsPath        = "/api/v1/automations/{automation_id}/runs"
	RunPath         = "/api/v1/automations/{automation_id}/runs/{run_id}"
	CancelPath      = "/api/v1/automations/{automation_id}/runs/{run_id}/cancel"
)

// Served lists every route of this package, for cmd/api's route table.
func Served() []string {
	return []string{
		http.MethodGet + " " + ListPath,
		http.MethodPost + " " + ListPath,
		http.MethodGet + " " + EligibilityPath,
		http.MethodGet + " " + OnePath,
		http.MethodPatch + " " + OnePath,
		http.MethodDelete + " " + OnePath,
		http.MethodPost + " " + RunNowPath,
		http.MethodPost + " " + TriggersPath,
		http.MethodPatch + " " + TriggerPath,
		http.MethodDelete + " " + TriggerPath,
		http.MethodGet + " " + RunsPath,
		http.MethodGet + " " + RunPath,
		http.MethodPost + " " + CancelPath,
	}
}

// WithBodies lists the routes that read a JSON body.
func WithBodies() []string {
	return []string{
		http.MethodPost + " " + ListPath,
		http.MethodPatch + " " + OnePath,
		http.MethodPost + " " + TriggersPath,
		http.MethodPatch + " " + TriggerPath,
	}
}

// What a person is told.
const (
	automationAbsent = "Automation not found"
	triggerAbsent    = "Trigger not found"
	runAbsent        = "Run not found"
	runFinished      = "This run already finished."
	notActive        = "This automation is paused. Resume it to run it."
	alreadyRunning   = "This automation already has a run waiting or running, and it is set to skip a run that starts then."
)
