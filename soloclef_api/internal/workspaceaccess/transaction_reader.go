package workspaceaccess

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Rows is the pair of reads both gates need, so a route can be given either the
// pool or its own transaction without knowing which.
type Rows interface {
	Membership(ctx context.Context, userID string, workspaceID int64) (*Membership, error)
	Workspace(ctx context.Context, workspaceID int64) (Workspace, error)
}

// TransactionReader reads the gates' two rows on a write's own transaction.
//
// It exists for the routes that decide a permission from a row they are about
// to write — `app/routes/logs_routes.py`'s three single-log writes load the log,
// then check the permission against the workspace the row names. The Python
// route does all of that on one session; a Go route that read the membership
// from the pool while holding a transaction would be asking for a *second*
// connection while holding one, which under load is a pool deadlock rather than
// a slow request.
type TransactionReader struct {
	Tx pgx.Tx
}

func (r TransactionReader) Membership(ctx context.Context, userID string, workspaceID int64) (*Membership, error) {
	row, err := New(r.Tx).CallerMembership(ctx, CallerMembershipParams{
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

func (r TransactionReader) Workspace(ctx context.Context, workspaceID int64) (Workspace, error) {
	row, err := New(r.Tx).WorkspaceGate(ctx, workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workspace{Exists: false}, nil
	}
	if err != nil {
		return Workspace{}, err
	}
	return Workspace{Exists: true, Name: row.Name, APIAccessEnabled: row.ApiAccessEnabled}, nil
}

// Both readers answer the same two questions.
var (
	_ Rows = PostgresReader{}
	_ Rows = TransactionReader{}
)
