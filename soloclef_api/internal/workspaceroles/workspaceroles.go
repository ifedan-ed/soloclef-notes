// Package workspaceroles serves a workspace's roles: the first write this tier
// answers, and the two reads beside it.
//
// The write is POST /api/v1/workspaces/{workspace_id}/roles
// (app/routes/rbac_routes.py:create_role). It was chosen as the first because
// of what it owes rather than what it costs: it is the simplest route in the
// API whose statements must all happen or none, and the inventory in
// custom/docs/go-api-tier.md shows it owes nothing outside its own rows — no
// event-bus publish, no Celery message, no notification, no PostHog capture.
// Every other write that owes one of those owes something this tier cannot
// send.
//
// What it does exercise is the whole of the write seam: a JSON body validated
// as pydantic validates it (internal/requestbody), four refusals in a fixed
// order, two statements that change rows plus one that reads, a 409 for a name
// already taken, and a rollback that must leave the workspace's existing
// default role alone.
package workspaceroles

import (
	"context"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/workspaceaccess"
)

// Access reads the caller's standing in the workspace, which every route here
// passes through the same two gates as the reads handed over before them.
type Access interface {
	Membership(ctx context.Context, userID string, workspaceID int64) (*workspaceaccess.Membership, error)
	Workspace(ctx context.Context, workspaceID int64) (workspaceaccess.Workspace, error)
}

// Role is a workspace_roles row as RoleRead renders it.
type Role struct {
	Name         string
	Description  *string
	Permissions  []string
	IsDefault    bool
	ID           int64
	WorkspaceID  int64
	IsSystemRole bool
	CreatedAt    time.Time
}
