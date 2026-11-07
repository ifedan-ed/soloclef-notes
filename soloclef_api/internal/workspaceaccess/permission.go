// Package workspaceaccess decides whether a caller may read something in a
// workspace, with the same rules and the same refusals as
// app.utils.rbac.check_permission.
//
// The order of the checks is part of the contract: membership first, then the
// permission, then the API-access gate. A caller who is not a member of a
// workspace that does not exist is told they have no access, not that the
// workspace is missing — which is also what stops the route from being a
// workspace-existence oracle.
package workspaceaccess

import (
	"net/http"
	"slices"
)

// FullAccess is Permission.FULL_ACCESS: the wildcard a role can hold.
const FullAccess = "*"

// LogsRead is Permission.LOGS_READ.
const LogsRead = "logs:read"

// Membership is what the rules need about a caller's place in a workspace.
type Membership struct {
	// IsOwner short-circuits to full access, as get_user_permissions does.
	IsOwner bool
	// RolePermissions is the role's permissions column, empty when the
	// membership has no role.
	RolePermissions []string
}

// Workspace is what the rules need about the workspace itself.
type Workspace struct {
	Exists           bool
	APIAccessEnabled bool
}

// Refusal is an HTTP status and the message the Python tier words it with.
type Refusal struct {
	Status  int
	Message string
}

func (r Refusal) Error() string { return r.Message }

// CheckMembership is the first half of check_permission: the caller must be a
// member and the membership must carry the permission. denialMessage is the
// route's own wording for a member who may not do this.
func CheckMembership(membership *Membership, permission, denialMessage string) *Refusal {
	if membership == nil {
		return &Refusal{Status: http.StatusForbidden, Message: "You don't have access to this workspace"}
	}

	permissions := membership.RolePermissions
	if membership.IsOwner {
		permissions = []string{FullAccess}
	}
	if !hasPermission(permissions, permission) {
		return &Refusal{Status: http.StatusForbidden, Message: denialMessage}
	}
	return nil
}

// CheckAPIAccess is _enforce_api_access_gate, which runs only once membership
// and the permission are settled. The workspace is therefore only looked up for
// a caller who is already a member, and a missing workspace is a 404 that
// nobody else can provoke.
func CheckAPIAccess(workspace Workspace, gated bool) *Refusal {
	if !workspace.Exists {
		return &Refusal{Status: http.StatusNotFound, Message: "Workspace not found"}
	}
	if gated && !workspace.APIAccessEnabled {
		return &Refusal{
			Status:  http.StatusForbidden,
			Message: "API access is not enabled for this workspace.",
		}
	}
	return nil
}

// hasPermission is app.db.has_permission.
func hasPermission(permissions []string, required string) bool {
	if len(permissions) == 0 {
		return false
	}
	if slices.Contains(permissions, FullAccess) {
		return true
	}
	return slices.Contains(permissions, required)
}
