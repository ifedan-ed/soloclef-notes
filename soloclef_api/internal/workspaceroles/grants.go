package workspaceroles

import (
	"slices"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/permissioncatalog"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/workspaceaccess"
)

// RolesCreate is Permission.ROLES_CREATE.
const RolesCreate = "roles:create"

// RolesRead is Permission.ROLES_READ.
const RolesRead = "roles:read"

// MayWriteRole is app/workspace_roles/grants.py:role_permissions_allowed.
//
// It reads stricter than it looks and that is the rule: only the workspace's
// owner may write a role at all, so a member holding roles:create still cannot,
// and no role may be given the wildcard, so ownership is never handed out
// through one. Both halves were checked against the Python tier, which refuses
// a member with roles:create in the same words as it refuses a wildcard.
func MayWriteRole(actor *workspaceaccess.Membership, permissions []string) bool {
	if actor == nil || !actor.IsOwner {
		return false
	}
	return !slices.Contains(permissions, permissioncatalog.FullAccess)
}

// UnknownPermission is the first permission of a requested role that the
// catalogue does not name, which the Python route refuses with a 400 naming it.
// It reports them in the order they were asked for, as the Python loop does.
func UnknownPermission(permissions []string) (string, bool) {
	for _, permission := range permissions {
		if !permissioncatalog.Known(permission) {
			return permission, true
		}
	}
	return "", false
}
