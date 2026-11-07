package workspacemembers

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Reader reads the roster from the three tables the Python route reads.
type Reader struct {
	Pool *pgxpool.Pool
}

// Roster runs the route's queries (queries.sql). The memberships are read
// first, on their own and unordered, because that is the query the Python route
// runs and the response's order is whatever it answers with; the roles and the
// accounts are then read by id, which is one query each instead of the route's
// one per membership.
func (r Reader) Roster(ctx context.Context, workspaceID int64) (Roster, error) {
	queries := New(r.Pool)

	rows, err := queries.Memberships(ctx, workspaceID)
	if err != nil {
		return Roster{}, err
	}

	roster := Roster{
		Memberships: make([]Membership, 0, len(rows)),
		Roles:       map[int64]Role{},
		Accounts:    map[string]Account{},
	}
	var (
		roleIDs    []int64
		accountIDs []string
	)
	for _, row := range rows {
		roster.Memberships = append(roster.Memberships, Membership{
			ID:          row.ID,
			UserID:      row.UserID,
			WorkspaceID: row.WorkspaceID,
			RoleID:      row.RoleID,
			IsOwner:     row.IsOwner,
			JoinedAt:    row.JoinedAt,
			CreatedAt:   row.CreatedAt,
		})
		if row.RoleID != nil {
			roleIDs = append(roleIDs, *row.RoleID)
		}
		accountIDs = append(accountIDs, row.UserID)
	}
	if len(roster.Memberships) == 0 {
		return roster, nil
	}

	if len(roleIDs) > 0 {
		roles, err := queries.RolesByID(ctx, roleIDs)
		if err != nil {
			return Roster{}, err
		}
		for _, row := range roles {
			roster.Roles[row.ID] = Role{
				ID:           row.ID,
				Name:         row.Name,
				Description:  row.Description,
				Permissions:  row.Permissions,
				IsDefault:    row.IsDefault,
				IsSystemRole: row.IsSystemRole,
				WorkspaceID:  row.WorkspaceID,
				CreatedAt:    row.CreatedAt,
			}
		}
	}

	accounts, err := queries.AccountsByID(ctx, accountIDs)
	if err != nil {
		return Roster{}, err
	}
	for _, row := range accounts {
		roster.Accounts[row.ID] = Account{
			Email:       row.Email,
			DisplayName: row.DisplayName,
			AvatarURL:   row.AvatarUrl,
			LastLogin:   row.LastLogin,
		}
	}
	return roster, nil
}
