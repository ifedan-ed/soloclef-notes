package produce

import "git.danvics.com/danvics/soloclef/soloclef_celery/celery"

// LogContext is the publishing request's own log context, which this app
// carries into every task it enqueues (app/observability/logs/celery_signals.py's
// _propagate_context).
//
// It is the one header neither tier can leave out: a task published without it
// logs with no request behind it, and the envelope is not the one the Python
// tier writes. Which fields are set is not this package's choice either — the
// Python middleware binds the request id always, the user id once the
// credential is resolved, and the workspace id only when the path names one
// (_WORKSPACE_IN_PATH), so a route whose path carries no workspace publishes
// without that field and so must a route served here.
type LogContext struct {
	RequestID   string
	UserID      string
	WorkspaceID string
	ThreadID    string
	JobID       string
}

// fields is the header's object: the fields _PROPAGATED names, in its order
// and not sorted, with the empty ones left out as `if ctx.get(k)` leaves them
// out. All of them empty is no header at all, as `if carried` decides.
func (c LogContext) fields() []celery.LogField {
	ordered := []celery.LogField{
		{Key: "request_id", Value: c.RequestID},
		{Key: "user_id", Value: c.UserID},
		{Key: "workspace_id", Value: c.WorkspaceID},
		{Key: "thread_id", Value: c.ThreadID},
		{Key: "job_id", Value: c.JobID},
	}
	set := make([]celery.LogField, 0, len(ordered))
	for _, field := range ordered {
		if field.Value != "" {
			set = append(set, field)
		}
	}
	return set
}
