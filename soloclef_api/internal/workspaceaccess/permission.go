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

// MembersView is Permission.MEMBERS_VIEW.
const MembersView = "members:view"

// Membership is a caller's place in a workspace, as get_user_membership loads
// it: the membership row and the role it points at.
type Membership struct {
	// IsOwner short-circuits to full access, as get_user_permissions does.
	IsOwner bool
	// RoleName is the role's name, absent when the membership has no role.
	RoleName *string
	// RolePermissions is the role's permissions column, empty when the
	// membership has no role.
	RolePermissions []string
}

// Workspace is the workspace row the gate looks up.
type Workspace struct {
	Exists           bool
	Name             string
	APIAccessEnabled bool
}

// Refusal is an HTTP status and the message the Python tier words it with.
type Refusal struct {
	Status  int
	Message string
}

func (r Refusal) Error() string { return r.Message }

// CheckAccess is the first half of check_workspace_access: the caller must be a
// member, and no particular permission is required.
func CheckAccess(membership *Membership) *Refusal {
	if membership == nil {
		return &Refusal{Status: http.StatusForbidden, Message: "You don't have access to this workspace"}
	}
	return nil
}

// CheckMembership is the first half of check_permission: the caller must be a
// member and the membership must carry the permission. denialMessage is the
// route's own wording for a member who may not do this.
func CheckMembership(membership *Membership, permission, denialMessage string) *Refusal {
	if refusal := CheckAccess(membership); refusal != nil {
		return refusal
	}
	if !hasPermission(EffectivePermissions(membership), permission) {
		return &Refusal{Status: http.StatusForbidden, Message: denialMessage}
	}
	return nil
}

// EffectivePermissions is get_user_permissions: an owner holds the wildcard
// whatever their role says, a membership with a role holds the role's
// permissions, and one without a role holds nothing.
func EffectivePermissions(membership *Membership) []string {
	switch {
	case membership == nil:
		return nil
	case membership.IsOwner:
		return []string{FullAccess}
	default:
		return membership.RolePermissions
	}
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
