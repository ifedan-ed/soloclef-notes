package folders

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/workspaceaccess"
)

// Access reads the caller's standing in the workspace a folder belongs to. It
// is workspaceaccess.Rows under this slice's own name, so a route reads it from
// the pool and a write reads it from its own transaction.
type Access = workspaceaccess.Rows

// checkGates is check_permission: membership and the permission first, then
// the API-access gate. It lives here rather than once per route because a
// second copy would be a second place for this tier's idea of what a caller may
// do to drift from the Python tier's.
//
// Seven of the nine routes run it *after* loading the folder, because the
// workspace it is asked about is the one the row names. The create runs it
// first, on the workspace the body names, and that is the Python module's own
// split rather than a choice here.
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

// parseFolderID reads the one path parameter seven of the routes share, as
// FastAPI coerces it (internal/requestnumber).
func parseFolderID(ctx huma.Context) (int64, *errorenvelope.Failure, error) {
	return requestnumber.Parse(ctx.Param("folder_id"), "path", "folder_id")
}
