// Package myaccess serves GET /api/v1/workspaces/{workspace_id}/my-access, the
// caller's own standing in a workspace: what the web app reads before it decides
// which of a page's controls exist.
package myaccess

import (
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/workspaceaccess"
)

// Build renders the Python route's UserWorkspaceAccess, key for key and in
// pydantic's field order. It carries no datetime, so there is nothing here that
// depends on how one is spelled.
func Build(workspaceID int64, workspace workspaceaccess.Workspace, membership *workspaceaccess.Membership) *pythonjson.Object {
	access := pythonjson.NewObject()
	access.Set("workspace_id", workspaceID)
	access.Set("workspace_name", workspace.Name)
	access.Set("is_owner", membership.IsOwner)
	if membership.RoleName == nil {
		access.Set("role_name", nil)
	} else {
		access.Set("role_name", *membership.RoleName)
	}
	granted := workspaceaccess.EffectivePermissions(membership)
	permissions := make([]any, 0, len(granted))
	for _, permission := range granted {
		permissions = append(permissions, permission)
	}
	access.Set("permissions", permissions)
	return access
}
