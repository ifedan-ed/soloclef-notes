package chat

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/event"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/chat/parts"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/academic"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/deliverables"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/documents"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/pagereader"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/websearch"
)

// The step display: the one line above an answer that says what the
// assistant is doing ("Searching your sources") and, afterwards, what it
// did.
//
// Each visible tool call is one activity. The page is sent a snapshot of it
// when it starts, when it waits for approval and when it ends
// ("data-activity"), and the same snapshots are saved at the head of the
// message ("data-activities") so that a reloaded page draws the same line.
// A clock of the turn's working time travels beside them
// ("data-activity-timing").
//
// Three rules, each the page's own:
//
//   - A snapshot's identity (sequence, kind, category, icon, start time)
//     never changes; the page drops a later snapshot that disagrees with
//     the first. So a call that pauses for approval and finishes in a later
//     request is finished with the identity it was saved with (seed).
//   - A finished activity never goes back to running.
//   - The words come from this file, never from the model: a tool name the
//     model chose is not shown, and a tool with no entry here is "Using a
//     tool".

// hidden marks a tool whose calls are not shown as a step at all.
var hidden = Activity{}

// builtInActivities are the tools this package always knows. A turn adds
// its own (Built.Activities) for tools that exist only for that turn.
var builtInActivities = map[string]Activity{
	documents.ToolName:            {Kind: "knowledge.search", Active: "Searching your sources", Completed: "Searched your sources", Category: "research", Icon: "library"},
	websearch.Exa.ToolName():      {Kind: "web.search.exa", Active: "Searching the web with Exa", Completed: "Searched the web with Exa", Category: "research", Icon: "search", IntegrationSource: "native", IntegrationKey: "exa"},
	websearch.Tavily.ToolName():   {Kind: "web.search.tavily", Active: "Searching the web with Tavily", Completed: "Searched the web with Tavily", Category: "research", Icon: "search", IntegrationSource: "native", IntegrationKey: "tavily"},
	academic.PubMedTool:           {Kind: "academic.pubmed", Active: "Searching PubMed", Completed: "Searched PubMed", Category: "research", Icon: "search"},
	academic.ArxivTool:            {Kind: "academic.arxiv", Active: "Searching arXiv", Completed: "Searched arXiv", Category: "research", Icon: "search"},
	academic.PreprintTool:         {Kind: "academic.preprints", Active: "Searching preprints", Completed: "Searched preprints", Category: "research", Icon: "search"},
	deliverables.DelegateName:     {Kind: "artifact.create", Active: "Generating artifact", Completed: "Generated artifact", Category: "artifact", Icon: "file-output"},
	deliverables.SaveTool:         {Kind: "artifact.save", Active: "Preparing the file", Completed: "Presented file", Category: "artifact", Icon: "file-output"},
	deliverables.PodcastTool:      {Kind: "artifact.podcast", Active: "Creating the podcast", Completed: "Created the podcast", Category: "artifact", Icon: "microphone"},
	pagereader.ToolName:           {Kind: "web.read", Active: "Reading the page", Completed: "Read the page", Category: "research", Icon: "scan-text", IntegrationSource: "native", IntegrationKey: "web"},
	deliverables.ImageTool:        {Kind: "generate_image", Active: "Creating an image", Completed: "Created an image", Category: "artifact", Icon: "image"},
	deliverables.VideoTool:        {Kind: "enqueue_deliverable_job", Active: "Starting video generation", Completed: "Video generation in progress", Category: "artifact", Icon: "clapperboard"},
	deliverables.ExecuteTool:      {Kind: "sandbox.execute", Active: "Running command", Completed: "Ran command", Category: "action", Icon: "terminal"},
	deliverables.ReadFileTool:     {Kind: "artifact.review", Active: "Reviewing the artifact", Completed: "Reviewed the artifact", Category: "artifact", Icon: "file-text"},
	deliverables.VerifyTool:       {Kind: "artifact.verify", Active: "Checking the artifact", Completed: "Checked the artifact", Category: "artifact", Icon: "badge-check"},
	deliverables.InstructionsTool: hidden,
}

// fallbackActivity is the line for a tool nothing describes.
var fallbackActivity = Activity{Kind: "tool.action", Active: "Using a tool", Completed: "Completed an action", Category: "action", Icon: "tool"}

// The statuses the page knows.
const (
	activityRunning     = "running"
	activityAwaiting    = "awaiting_approval"
	activityCompleted   = "completed"
	activityFailed      = "error"
	activityCancelled   = "cancelled"
	activityInterrupted = "interrupted"
)

// activityTitle is an activity's line for a status.
func activityTitle(a Activity, status string) string {
	switch status {
	case activityCompleted:
		return a.Completed
	case activityAwaiting:
		return "Approval needed: " + a.Active
	case activityFailed:
		return "Couldn't complete: " + a.Active
	case activityCancelled:
		return "Stopped: " + a.Active
	case activityInterrupted:
		return "Interrupted: " + a.Active
	}
	return a.Active
}

// integration is the service an activity is done with, as the page reads it.
type integration struct {
	Source string `json:"source"`
	Key    string `json:"key,omitempty"`
	Name   string `json:"name,omitempty"`
}

// activitySnapshot is one activity as the page is sent it and as it is
// saved. The field names are the page's.
type activitySnapshot struct {
	ID          string       `json:"id"`
	Sequence    int          `json:"sequence"`
	Kind        string       `json:"kind"`
	Status      string       `json:"status"`
	Title       string       `json:"title"`
	Category    string       `json:"category"`
	IconKey     string       `json:"iconKey"`
	StartedAt   string       `json:"startedAt"`
	CompletedAt string       `json:"completedAt,omitempty"`
	Integration *integration `json:"integration,omitempty"`
}

func terminal(status string) bool {
	return status != activityRunning && status != activityAwaiting
}

// activityTiming is the turn's working time: how long the assistant has
// been at it, not counting time spent waiting for a person.
type activityTiming struct {
	Status           string `json:"status"`
	ActiveDurationMs int64  `json:"activeDurationMs"`
}

// activityID is a call's activity id. It is a function of the call's id and
// nothing else, so the frame that draws a call's card and the snapshot that
// describes its work name the same activity without either having seen the
// other, in this request or in the one that resumes it.
func activityID(callID string) string {
	return "activity-" + callID
}

// journal is one answer's activities.
type journal struct {
	now func() time.Time
	// extra are the turn's own tools.
	extra map[string]Activity

	mu sync.Mutex
	// byID holds every activity this stretch knows: the ones it started and
	// the ones it was seeded with. order lists the ones it started, which
	// are the ones saved with its own message.
	byID  map[string]*activitySnapshot
	spec  map[string]Activity
	order []string
	// seeded are activities of the message that paused, finished in this
	// stretch; changed lists those whose saved copy must be rewritten.
	seeded  map[string]bool
	changed map[string]bool
	next    int

	// The clock: base is what earlier stretches of the turn used, since is
	// when this one began, and stopped is its final reading.
	base    time.Duration
	since   time.Time
	stopped *activityTiming

	version uint64
}

func newJournal(now func() time.Time, extra map[string]Activity) *journal {
	return &journal{
		now: now, extra: extra, since: now(),
		byID: map[string]*activitySnapshot{}, spec: map[string]Activity{}, seeded: map[string]bool{}, changed: map[string]bool{},
	}
}

// describe returns how a tool's work is shown, and whether it is shown.
func (j *journal) describe(tool string) (Activity, bool) {
	if a, known := j.extra[tool]; known {
		return a, a != hidden
	}
	if a, known := builtInActivities[tool]; known {
		return a, a != hidden
	}
	return fallbackActivity, true
}

// metadata is what a tool call's card carries so the page can tie the card
// to its activity. It has the signature the stream writer and the parts
// builder ask for.
func (j *journal) metadata(ev event.Event) any {
	if _, shown := j.describe(ev.Tool); !shown {
		return nil
	}
	return map[string]string{"activityId": activityID(ev.CallID)}
}

func (j *journal) stamp() string {
	return j.now().UTC().Format("2006-01-02T15:04:05.000000+00:00")
}

// observe applies one event and returns the snapshot to send, if the event
// changed an activity.
func (j *journal) observe(ev event.Event) (activitySnapshot, bool) {
	switch ev.Kind {
	case event.ToolCallReady:
		return j.start(ev)
	case event.ApprovalNeeded:
		return j.move(ev, activityAwaiting)
	case event.ToolResult:
		if ev.IsError {
			return j.move(ev, activityFailed)
		}
		return j.move(ev, activityCompleted)
	}
	return activitySnapshot{}, false
}

func (j *journal) start(ev event.Event) (activitySnapshot, bool) {
	spec, shown := j.describe(ev.Tool)
	if !shown {
		return activitySnapshot{}, false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	id := activityID(ev.CallID)
	if _, known := j.byID[id]; known {
		return activitySnapshot{}, false
	}
	j.next++
	snapshot := &activitySnapshot{
		ID: id, Sequence: j.next, Kind: spec.Kind, Status: activityRunning, Title: activityTitle(spec, activityRunning),
		Category: spec.Category, IconKey: spec.Icon, StartedAt: j.stamp(),
	}
	if spec.IntegrationSource != "" {
		snapshot.Integration = &integration{Source: spec.IntegrationSource, Key: spec.IntegrationKey, Name: spec.IntegrationName}
	}
	j.byID[id], j.spec[id] = snapshot, spec
	j.order = append(j.order, id)
	j.version++
	return *snapshot, true
}

// move changes an activity's status. A finished activity stays finished.
func (j *journal) move(ev event.Event, status string) (activitySnapshot, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	id := activityID(ev.CallID)
	snapshot, known := j.byID[id]
	if !known || terminal(snapshot.Status) || snapshot.Status == status {
		return activitySnapshot{}, false
	}
	j.set(id, snapshot, status)
	return *snapshot, true
}

func (j *journal) set(id string, snapshot *activitySnapshot, status string) {
	snapshot.Status = status
	if spec, described := j.spec[id]; described {
		snapshot.Title = activityTitle(spec, status)
	}
	if terminal(status) {
		snapshot.CompletedAt = j.stamp()
	}
	if j.seeded[id] {
		j.changed[id] = true
	}
	j.version++
}

// close ends the stretch: whatever is still running did not finish, and the
// clock stops. stopped says the person pressed Stop; paused says the turn
// is waiting for them. It returns the snapshots that changed and the
// clock's final reading.
func (j *journal) close(stopped, paused bool) ([]activitySnapshot, activityTiming) {
	j.mu.Lock()
	defer j.mu.Unlock()
	var out []activitySnapshot
	for _, id := range j.order {
		snapshot := j.byID[id]
		if snapshot.Status != activityRunning {
			continue
		}
		status := activityInterrupted
		if stopped {
			status = activityCancelled
		}
		j.set(id, snapshot, status)
		out = append(out, *snapshot)
	}
	timing := activityTiming{Status: "completed", ActiveDurationMs: (j.base + j.now().Sub(j.since)).Milliseconds()}
	if paused {
		timing.Status = "paused"
	}
	j.stopped = &timing
	j.version++
	return out, timing
}

// timing is the clock's reading now.
func (j *journal) timing() activityTiming {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.timingLocked()
}

func (j *journal) timingLocked() activityTiming {
	if j.stopped != nil {
		return *j.stopped
	}
	return activityTiming{Status: "running", ActiveDurationMs: (j.base + j.now().Sub(j.since)).Milliseconds()}
}

// activitiesPart is the saved form: the message part the page reads the
// step display from after a reload.
type activitiesPart struct {
	Type string `json:"type"`
	Data struct {
		Activities []activitySnapshot `json:"activities"`
		Timing     *activityTiming    `json:"timing,omitempty"`
	} `json:"data"`
}

const activitiesPartType = "data-activities"

// part is the stretch's own activities and its clock as a message part.
// Every answer has one, an answer that used no tool included: the page
// builds the part from the first clock frame whether or not a step follows,
// and what is saved must be what the page built.
func (j *journal) part() activitiesPart {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := activitiesPart{Type: activitiesPartType}
	out.Data.Activities = make([]activitySnapshot, 0, len(j.order))
	for _, id := range j.order {
		out.Data.Activities = append(out.Data.Activities, *j.byID[id])
	}
	timing := j.timingLocked()
	out.Data.Timing = &timing
	return out
}

func (j *journal) changes() uint64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.version
}

// seed loads the activities and the clock of the message that paused, so
// that the calls it left waiting are finished under the identity they were
// saved with and the clock carries on from where it stopped.
func (j *journal) seed(content []byte) {
	var parts []json.RawMessage
	if json.Unmarshal(content, &parts) != nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, raw := range parts {
		var part activitiesPart
		if json.Unmarshal(raw, &part) != nil || part.Type != activitiesPartType {
			continue
		}
		for i := range part.Data.Activities {
			snapshot := part.Data.Activities[i]
			if _, known := j.byID[snapshot.ID]; known || snapshot.ID == "" {
				continue
			}
			j.byID[snapshot.ID] = &snapshot
			j.seeded[snapshot.ID] = true
			// The words of a seeded activity are rebuilt from its saved
			// lines: "Approval needed: X" finishes as the completed line of
			// whatever tool has the active line X.
			j.spec[snapshot.ID] = specOf(snapshot, j.extra)
			if snapshot.Sequence > j.next {
				j.next = snapshot.Sequence
			}
		}
		if part.Data.Timing != nil {
			j.base = time.Duration(part.Data.Timing.ActiveDurationMs) * time.Millisecond
		}
	}
}

// specOf finds the description a saved snapshot was made from, by its kind.
// A kind nothing describes any more keeps its saved words for every status.
func specOf(snapshot activitySnapshot, extra map[string]Activity) Activity {
	for _, catalogue := range []map[string]Activity{extra, builtInActivities} {
		for _, spec := range catalogue {
			if spec.Kind == snapshot.Kind && spec.Kind != "" {
				return spec
			}
		}
	}
	if snapshot.Kind == fallbackActivity.Kind {
		return fallbackActivity
	}
	active := strings.TrimPrefix(snapshot.Title, "Approval needed: ")
	return Activity{Kind: snapshot.Kind, Active: active, Completed: active, Category: snapshot.Category, Icon: snapshot.IconKey}
}

// fillActivities rewrites, in a saved message's content, the activities
// this stretch finished for it. changed is false when there was nothing to
// rewrite.
func (j *journal) fillActivities(content []byte) (filled []byte, changed bool, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.changed) == 0 {
		return content, false, nil
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(content, &parts); err != nil {
		return nil, false, err
	}
	for i, raw := range parts {
		var part activitiesPart
		if json.Unmarshal(raw, &part) != nil || part.Type != activitiesPartType {
			continue
		}
		for k := range part.Data.Activities {
			if id := part.Data.Activities[k].ID; j.changed[id] {
				part.Data.Activities[k] = *j.byID[id]
				changed = true
			}
		}
		if parts[i], err = marshalPlain(part); err != nil {
			return nil, false, err
		}
	}
	if !changed {
		return content, false, nil
	}
	filled, err = marshalPlain(parts)
	return filled, true, err
}

// marshalPlain encodes as everything in a message's content is encoded.
func marshalPlain(value any) ([]byte, error) { return parts.Encode(value) }
