package workspaceroles

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/workspaceaccess"
)

// Every route in this slice is gated the same way and differs only in which
// permission it asks for, so the gates live here rather than once per route: a
// second copy of them would be a second place for this tier's idea of what a
// caller may do to drift from the Python tier's.

// parsePath reads the route's path parameters as FastAPI coerces them and
// reports every bad one together, which is what a request with two unreadable
// ids gets from the Python tier — one 422 naming both, in the route's own
// parameter order.
func parsePath(ctx huma.Context, names ...string) ([]int64, []errorenvelope.Failure, error) {
	values := make([]int64, len(names))
	failures := make([]errorenvelope.Failure, 0, len(names))
	var outOfRange error
	for index, name := range names {
		value, failure, rangeErr := requestnumber.Parse(ctx.Param(name), "path", name)
		values[index] = value
		if failure != nil {
			failures = append(failures, *failure)
		}
		if rangeErr != nil {
			outOfRange = rangeErr
		}
	}
	return values, failures, outOfRange
}

// checkGates is check_permission: membership and the permission first, then the
// API-access gate. The order is load-bearing — the workspace is only looked up
// for a caller who is already a member — and the membership it returns is the
// `actor` the role rules are then asked about.
func checkGates(
	ctx context.Context,
	access Access,
	caller principal.Principal,
	workspaceID int64,
	permission, denial string,
) (*workspaceaccess.Membership, *workspaceaccess.Refusal, error) {
	membership, err := access.Membership(ctx, caller.UserID, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	if refusal := workspaceaccess.CheckMembership(membership, permission, denial); refusal != nil {
		return nil, refusal, nil
	}
	workspace, err := access.Workspace(ctx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	if refusal := workspaceaccess.CheckAPIAccess(workspace, caller.IsGated()); refusal != nil {
		return nil, refusal, nil
	}
	return membership, nil, nil
}
