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
// with the role's permissions, or nil when there is none.
func (r PostgresReader) Membership(ctx context.Context, userID string, workspaceID int64) (*Membership, error) {
	var membership Membership
	err := r.Pool.QueryRow(ctx, `
		SELECT member.is_owner, COALESCE(role.permissions, '{}')
		FROM workspace_memberships AS member
		LEFT JOIN workspace_roles AS role ON role.id = member.role_id
		WHERE member.user_id = $1::uuid AND member.workspace_id = $2
		ORDER BY member.id
		LIMIT 1`, userID, workspaceID,
	).Scan(&membership.IsOwner, &membership.RolePermissions)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &membership, nil
}

// Workspace is what _enforce_api_access_gate needs: whether the workspace is
// there at all, and whether it is open to API keys.
func (r PostgresReader) Workspace(ctx context.Context, workspaceID int64) (Workspace, error) {
	var workspace Workspace
	err := r.Pool.QueryRow(ctx,
		`SELECT api_access_enabled FROM workspaces WHERE id = $1`, workspaceID,
	).Scan(&workspace.APIAccessEnabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workspace{Exists: false}, nil
	}
	if err != nil {
		return Workspace{}, err
	}
	workspace.Exists = true
	return workspace, nil
}
