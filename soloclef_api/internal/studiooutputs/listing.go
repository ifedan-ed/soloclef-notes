package studiooutputs

import (
	"encoding/json"
	"math"
	"time"
)

// output is one studio output as both apps read it.
type output struct {
	ID              int64           `json:"id"`
	Kind            string          `json:"kind"`
	Title           string          `json:"title"`
	Status          string          `json:"status"`
	Phase           *string         `json:"phase"`
	Progress        *int64          `json:"progress"`
	FailureCode     *string         `json:"failure_code"`
	ArtifactID      *int64          `json:"artifact_id"`
	ArtifactFormat  *string         `json:"artifact_format"`
	DurationSeconds *int64          `json:"duration_seconds"`
	SourceCount     int             `json:"source_count"`
	Options         json.RawMessage `json:"options"`
	GeneratedBy     json.RawMessage `json:"generated_by"`
	CreatedAt       time.Time       `json:"created_at"`
}

// A podcast's status, as the studio's five states and the phase within
// "running" the panel shows.
var podcastStates = map[string]struct{ status, phase string }{
	"pending":         {"queued", ""},
	"awaiting_brief":  {"queued", ""},
	"drafting":        {"running", "drafting"},
	"awaiting_review": {"running", "drafting"},
	"rendering":       {"running", "rendering"},
	"ready":           {"ready", ""},
	"failed":          {"failed", ""},
	"cancelled":       {"cancelled", ""},
}

// A deliverable job's status as a studio state. A job asked to stop is
// still running until its worker says it has.
var jobStates = map[string]string{
	"queued": "queued", "running": "running", "cancelling": "running",
	"cancelled": "cancelled", "failed": "failed", "ready": "ready",
}

// outputOf is one row as the panel reads it. An output whose run is gone
// (deleted, or never started) is "failed": there is nothing that will
// finish it.
func outputOf(row OutputsRow) output {
	out := output{
		ID: row.ID, Kind: row.Kind, Title: row.Title, Status: "failed",
		Options: objectOrEmpty(row.Options), GeneratedBy: objectOrNull(row.GeneratedBy),
		SourceCount: countOf(row.SourceDocumentIds), CreatedAt: row.CreatedAt,
	}
	if status, ok := jsonText(row.PodcastStatus); ok {
		state := podcastStates[status]
		if state.status != "" {
			out.Status = state.status
		}
		if state.phase != "" {
			phase := state.phase
			out.Phase = &phase
		}
		out.ArtifactID, out.DurationSeconds = row.PodcastArtifact, row.PodcastDuration
	} else if status, ok := jsonText(row.JobStatus); ok {
		if state := jobStates[status]; state != "" {
			out.Status = state
		}
		out.Phase, out.Progress, out.FailureCode, out.ArtifactID = row.JobPhase, row.JobProgress, row.JobFailure, row.JobArtifact
	}
	if out.ArtifactID != nil {
		out.ArtifactFormat = row.ArtifactFormat
		if out.DurationSeconds == nil {
			out.DurationSeconds = recordedDuration(row.ArtifactMetadata)
		}
	}
	return out
}

func jsonText(raw []byte) (string, bool) {
	var text string
	if len(raw) == 0 || json.Unmarshal(raw, &text) != nil {
		return "", false
	}
	return text, true
}

// recordedDuration is the whole seconds a render recorded in the artifact's
// metadata, or nothing when it recorded none that is a positive number.
func recordedDuration(metadata []byte) *int64 {
	var fields map[string]any
	if json.Unmarshal(metadata, &fields) != nil {
		return nil
	}
	seconds, ok := fields["duration_seconds"].(float64)
	if !ok || seconds <= 0 {
		return nil
	}
	whole := int64(math.Round(seconds))
	return &whole
}

// objectOrEmpty is a jsonb object, or {} for a value that is not one, so
// one odd row never fails the list.
func objectOrEmpty(raw []byte) json.RawMessage {
	var fields map[string]any
	if json.Unmarshal(raw, &fields) != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func objectOrNull(raw []byte) json.RawMessage {
	var fields map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &fields) != nil || fields == nil {
		return json.RawMessage(`null`)
	}
	return raw
}

// countOf is how many sources an output was made from.
func countOf(raw []byte) int {
	var items []any
	if json.Unmarshal(raw, &items) != nil {
		return 0
	}
	return len(items)
}
