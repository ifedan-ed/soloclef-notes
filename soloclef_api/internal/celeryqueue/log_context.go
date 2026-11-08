package celeryqueue

import (
	"regexp"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// workspaceInPath is app/observability/logs/access.py's _WORKSPACE_IN_PATH.
var workspaceInPath = regexp.MustCompile(`/workspaces/([0-9]+)(/|$)`)

// LogContextFor is the log context the Python tier would have bound for this
// request, and so the one a task published during it has to carry.
//
// Two places bind it there and both are mirrored: RequestContextMiddleware
// binds the request id for every request and the workspace id only when the
// *path* names one, and get_auth_context binds the user id once the credential
// is resolved. The path rule is the surprising half — a route that knows which
// workspace it is working in still publishes without the field unless the path
// says so, which is why this takes the path rather than the route's own
// workspace.
func LogContextFor(path, requestID, userID string) produce.LogContext {
	context := produce.LogContext{RequestID: requestID, UserID: userID}
	if found := workspaceInPath.FindStringSubmatch(path); found != nil {
		context.WorkspaceID = found[1]
	}
	return context
}
