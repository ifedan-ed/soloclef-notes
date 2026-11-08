package workspaceroles

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store reads and writes workspace_roles. Its read methods take the pool and
// its write methods take a transaction, which is the seam: a write's statements
// all run on the pgx.Tx that internal/database.Write opened, so none of them
// can outlive a rollback.
type Store struct {
	Pool *pgxpool.Pool
}

// Roles is list_roles' query: every role of the workspace, unordered, so the
// array's order is the one the Python route gets for the same question.
func (s Store) Roles(ctx context.Context, workspaceID int64) ([]Role, error) {
	rows, err := New(s.Pool).RolesInWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	roles := make([]Role, 0, len(rows))
	for _, row := range rows {
		roles = append(roles, Role{
			Name:         row.Name,
			Description:  row.Description,
			Permissions:  row.Permissions,
			IsDefault:    row.IsDefault,
			ID:           row.ID,
			WorkspaceID:  row.WorkspaceID,
			IsSystemRole: row.IsSystemRole,
			CreatedAt:    row.CreatedAt,
		})
	}
	return roles, nil
}

// Role is get_role's query. A role of another workspace is reported absent,
// which is what makes the route not a role-existence oracle.
func (s Store) Role(ctx context.Context, workspaceID, roleID int64) (*Role, error) {
	row, err := New(s.Pool).RoleInWorkspace(ctx, RoleInWorkspaceParams{
		ID:          roleID,
		WorkspaceID: workspaceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Role{
		Name:         row.Name,
		Description:  row.Description,
		Permissions:  row.Permissions,
		IsDefault:    row.IsDefault,
		ID:           row.ID,
		WorkspaceID:  row.WorkspaceID,
		IsSystemRole: row.IsSystemRole,
		CreatedAt:    row.CreatedAt,
	}, nil
}

// NameTaken is create_role's 409 check, inside the write's transaction.
func NameTaken(ctx context.Context, tx pgx.Tx, workspaceID int64, name string) (bool, error) {
	return New(tx).RoleNameTaken(ctx, RoleNameTakenParams{
		WorkspaceID: workspaceID,
		Name:        name,
	})
}

// ClearDefault unsets whatever role the workspace currently has as its default.
func ClearDefault(ctx context.Context, tx pgx.Tx, workspaceID int64) error {
	return New(tx).ClearDefaultRole(ctx, workspaceID)
}

// Insert writes the new role and returns the row as it was stored, which is
// what RoleRead renders — the Python route returns the refreshed instance for
// the same reason.
func Insert(ctx context.Context, tx pgx.Tx, workspaceID int64, requested Requested, createdAt time.Time) (Role, error) {
	row, err := New(tx).CreateRole(ctx, CreateRoleParams{
		Name:        requested.Name,
		Description: requested.Description,
		Permissions: requested.Permissions,
		IsDefault:   requested.IsDefault,
		WorkspaceID: workspaceID,
		CreatedAt:   createdAt,
	})
	if err != nil {
		return Role{}, err
	}
	return Role{
		Name:         row.Name,
		Description:  row.Description,
		Permissions:  row.Permissions,
		IsDefault:    row.IsDefault,
		ID:           row.ID,
		WorkspaceID:  row.WorkspaceID,
		IsSystemRole: row.IsSystemRole,
		CreatedAt:    row.CreatedAt,
	}, nil
}

// RoleIn is the same question as Role, asked inside a write's transaction: the
// Python routes load the role from the session they then write through.
func RoleIn(ctx context.Context, tx pgx.Tx, workspaceID, roleID int64) (*Role, error) {
	row, err := New(tx).RoleInWorkspace(ctx, RoleInWorkspaceParams{
		ID:          roleID,
		WorkspaceID: workspaceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Role{
		Name:         row.Name,
		Description:  row.Description,
		Permissions:  row.Permissions,
		IsDefault:    row.IsDefault,
		ID:           row.ID,
		WorkspaceID:  row.WorkspaceID,
		IsSystemRole: row.IsSystemRole,
		CreatedAt:    row.CreatedAt,
	}, nil
}

// NameTakenElsewhere is update_role's 409 check, asked exactly as the Python
// route asks it — the whole workspace, not excluding the role being renamed.
func NameTakenElsewhere(ctx context.Context, tx pgx.Tx, workspaceID int64, name *string) (bool, error) {
	return New(tx).RoleNameTakenElsewhere(ctx, RoleNameTakenElsewhereParams{
		WorkspaceID: workspaceID,
		Name:        name,
	})
}

// ClearOtherDefaults is update_role's "unset existing default".
func ClearOtherDefaults(ctx context.Context, tx pgx.Tx, workspaceID int64) error {
	return New(tx).ClearOtherDefaultRoles(ctx, workspaceID)
}

// Update writes the fields the body mentioned and returns the row as stored,
// which is what RoleRead renders — the Python route returns the refreshed
// instance for the same reason.
func Update(ctx context.Context, tx pgx.Tx, workspaceID, roleID int64, changes Changes) (Role, error) {
	row, err := New(tx).UpdateRole(ctx, UpdateRoleParams{
		SetName:        changes.Name.Present,
		Name:           changes.Name.Value,
		SetDescription: changes.Description.Present,
		Description:    changes.Description.Value,
		SetPermissions: changes.Permissions.Present,
		Permissions:    changes.Permissions.Value,
		SetIsDefault:   changes.IsDefault.Present,
		IsDefault:      changes.IsDefault.Value,
		ID:             roleID,
		WorkspaceID:    workspaceID,
	})
	if err != nil {
		return Role{}, err
	}
	return Role{
		Name:         row.Name,
		Description:  row.Description,
		Permissions:  row.Permissions,
		IsDefault:    row.IsDefault,
		ID:           row.ID,
		WorkspaceID:  row.WorkspaceID,
		IsSystemRole: row.IsSystemRole,
		CreatedAt:    row.CreatedAt,
	}, nil
}

// Delete removes the role, scoped to its workspace as the SELECT that loaded it
// was.
func Delete(ctx context.Context, tx pgx.Tx, workspaceID, roleID int64) error {
	return New(tx).DeleteRole(ctx, DeleteRoleParams{ID: roleID, WorkspaceID: workspaceID})
}
