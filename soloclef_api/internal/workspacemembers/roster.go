// Package workspacemembers serves GET /api/v1/workspaces/{workspace_id}/members,
// the member list every open dashboard re-reads on a timer.
package workspacemembers

import "time"

// Membership is one workspace_memberships row, as the Python route's own query
// returns it.
type Membership struct {
	ID          int64
	UserID      string
	WorkspaceID int64
	RoleID      *int64
	IsOwner     bool
	JoinedAt    time.Time
	CreatedAt   time.Time
}

// Role is a workspace_roles row, as the nested RoleRead renders it.
type Role struct {
	ID           int64
	Name         string
	Description  *string
	Permissions  []string
	IsDefault    bool
	IsSystemRole bool
	WorkspaceID  int64
	CreatedAt    time.Time
}

// Account is the part of a "user" row the response carries. The Python route
// reads one of these per membership; a membership whose account has been
// deleted gets nulls for all four, so an absent account is not an error.
type Account struct {
	Email       string
	DisplayName *string
	AvatarURL   *string
	LastLogin   *time.Time
}

// Roster is what one request reads: the memberships in the order the query
// returned them, which is the order of the response, and the roles and accounts
// they point at.
type Roster struct {
	Memberships []Membership
	Roles       map[int64]Role
	Accounts    map[string]Account
}
