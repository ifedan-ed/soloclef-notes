package workspacelogs

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/workspaceaccess"
)

// checkGates is check_permission: membership and the permission first, then the
// API-access gate. It lives here rather than once per route because a second
// copy would be a second place for this tier's idea of what a caller may do to
// drift from the Python tier's.
//
// Every route in this slice runs it *after* loading the log, because the
// workspace it is asked about is the one the row names. That is why it takes
// workspaceaccess.Rows rather than the pool: the read route asks the pool and a
// write asks its own transaction, so a write never wants a second connection
// while it holds one (workspaceaccess.TransactionReader).
func checkGates(
	ctx context.Context,
	access workspaceaccess.Rows,
	caller principal.Principal,
	workspaceID int64,
	permission, denial string,
) (*workspaceaccess.Refusal, error) {
	membership, err := access.Membership(ctx, caller.UserID, workspaceID)
	if err != nil {
		return nil, err
	}
	if refusal := workspaceaccess.CheckMembership(membership, permission, denial); refusal != nil {
		return refusal, nil
	}
	workspace, err := access.Workspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if refusal := workspaceaccess.CheckAPIAccess(workspace, caller.IsGated()); refusal != nil {
		return refusal, nil
	}
	return nil, nil
}

// parseLogID reads the one path parameter the three single-log routes share, as
// FastAPI coerces it (internal/requestnumber).
func parseLogID(ctx huma.Context) (int64, *errorenvelope.Failure, error) {
	return requestnumber.Parse(ctx.Param("log_id"), "path", "log_id")
}
