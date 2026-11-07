package workspaceaccess

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresReader reads membership and workspace state from the tables
// app.utils.rbac reads: workspace_memberships with its role, and workspaces.
type PostgresReader struct {
	Pool *pgxpool.Pool
}

// Membership is get_user_membership: the caller's membership of one workspace,
// with the role it points at, or nil when there is none.
func (r PostgresReader) Membership(ctx context.Context, userID string, workspaceID int64) (*Membership, error) {
	row, err := New(r.Pool).CallerMembership(ctx, CallerMembershipParams{
		UserID:      userID,
		WorkspaceID: workspaceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Membership{
		IsOwner:         row.IsOwner,
		RoleName:        row.RoleName,
		RolePermissions: row.RolePermissions,
	}, nil
}

// Workspace is the row _enforce_api_access_gate looks up: whether the workspace
// is there at all, whether it is open to API keys, and its name.
func (r PostgresReader) Workspace(ctx context.Context, workspaceID int64) (Workspace, error) {
	row, err := New(r.Pool).WorkspaceGate(ctx, workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workspace{Exists: false}, nil
	}
	if err != nil {
		return Workspace{}, err
	}
	return Workspace{Exists: true, Name: row.Name, APIAccessEnabled: row.ApiAccessEnabled}, nil
}
