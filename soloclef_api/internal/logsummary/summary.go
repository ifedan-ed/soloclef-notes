package logsummary

import (
	"fmt"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// Row is one logs row, as the Python route's query returns it.
type Row struct {
	ID        int64
	Status    string
	Level     string
	Message   string
	Source    *string
	CreatedAt time.Time
	// Metadata is the log_metadata column as stored. It is kept as text so the
	// key order survives, which is what the Python round trip through
	// json.loads and json.dumps preserves.
	Metadata []byte
}

// Status values are app.db.LogStatus; only these two are read here.
const (
	statusInProgress = "IN_PROGRESS"
	statusFailed     = "FAILED"
)

// recentFailureLimit is the Python route's own cap on recent_failures.
const recentFailureLimit = 10

// unknownTaskName is what the Python route puts in a summary entry whose
// metadata names no task.
const unknownTaskName = "Unknown"

// Build is the Python route's summary, key for key and in its order.
//
// The rows must arrive newest first, as the route's ORDER BY created_at DESC
// gives them, because that order decides the order of the counter keys and
// which ten failures are reported.
func Build(rows []Row, hours int64) (*pythonjson.Object, error) {
	byStatus := pythonjson.NewObject()
	byLevel := pythonjson.NewObject()
	bySource := pythonjson.NewObject()
	activeTasks := []any{}
	recentFailures := []any{}

	for _, row := range rows {
		increment(byStatus, row.Status)
		increment(byLevel, row.Level)
		// `if log.source:` — an empty string is not counted either.
		if row.Source != nil && *row.Source != "" {
			increment(bySource, *row.Source)
		}

		metadata, err := readMetadata(row)
		if err != nil {
			return nil, err
		}

		switch row.Status {
		case statusInProgress:
			task := pythonjson.NewObject()
			task.Set("id", row.ID)
			task.Set("task_name", metadata.taskName)
			task.Set("message", row.Message)
			task.Set("started_at", pythonjson.ISOFormat(row.CreatedAt))
			task.Set("source", sourceOrNil(row.Source))
			task.Set("document_id", metadata.documentID)
			task.Set("connector_id", metadata.connectorID)
			activeTasks = append(activeTasks, task)
		case statusFailed:
			if len(recentFailures) >= recentFailureLimit {
				break
			}
			failure := pythonjson.NewObject()
			failure.Set("id", row.ID)
			failure.Set("task_name", metadata.taskName)
			failure.Set("message", row.Message)
			failure.Set("failed_at", pythonjson.ISOFormat(row.CreatedAt))
			failure.Set("source", sourceOrNil(row.Source))
			failure.Set("error_details", metadata.errorDetails)
			recentFailures = append(recentFailures, failure)
		}
	}

	summary := pythonjson.NewObject()
	summary.Set("total_logs", len(rows))
	summary.Set("time_window_hours", hours)
	summary.Set("by_status", byStatus)
	summary.Set("by_level", byLevel)
	summary.Set("by_source", bySource)
	summary.Set("active_tasks", activeTasks)
	summary.Set("recent_failures", recentFailures)
	return summary, nil
}

func increment(counter *pythonjson.Object, key string) {
	current, _ := counter.Get(key)
	count, _ := current.(int)
	counter.Set(key, count+1)
}

func sourceOrNil(source *string) any {
	if source == nil {
		return nil
	}
	return *source
}

// metadataFields are the four values the route reads out of log_metadata.
type metadataFields struct {
	// taskName is whatever the metadata holds under that key, not necessarily
	// a string: the Python route passes the value through untouched.
	taskName     any
	documentID   any
	connectorID  any
	errorDetails any
}

// readMetadata mirrors the route's guarded reads. An absent or empty
// log_metadata is falsy in Python, so it yields the same values as no metadata
// at all; metadata that is not an object would raise there, and is an error
// here rather than a quietly different answer.
func readMetadata(row Row) (metadataFields, error) {
	fields := metadataFields{taskName: unknownTaskName}
	if len(row.Metadata) == 0 {
		return fields, nil
	}
	value, err := pythonjson.Decode(row.Metadata)
	if err != nil {
		return metadataFields{}, fmt.Errorf("log %d has unreadable metadata: %w", row.ID, err)
	}
	if value == nil {
		return fields, nil
	}
	object, isObject := value.(*pythonjson.Object)
	if !isObject {
		return metadataFields{}, fmt.Errorf("log %d has metadata that is not an object", row.ID)
	}
	if object.Len() == 0 {
		return fields, nil
	}
	if taskName := object.GetOrNil("task_name"); taskName != nil {
		fields.taskName = taskName
	}
	fields.documentID = object.GetOrNil("document_id")
	fields.connectorID = object.GetOrNil("connector_id")
	fields.errorDetails = object.GetOrNil("error_details")
	return fields, nil
}
