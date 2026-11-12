package agentrules

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the rules in Postgres. Every method takes the account first and
// passes it to a statement that names it: there is no method that reaches a
// rule by its id alone.
type Store struct {
	Pool *pgxpool.Pool
}

// The reasons a write is refused that are the caller's to hear.
var (
	// ErrDuplicate is a rule the account already has for the same scope.
	ErrDuplicate = errors.New("agentrules: an identical rule exists")
	// ErrNoThread is a thread that is not the account's.
	ErrNoThread = errors.New("agentrules: the thread is not the account's")
)

// Postgres's codes for a row a unique constraint refused and for one a
// foreign key refused. Only the first is a duplicate; a check that failed,
// or anything else, is some other fault and is not reported as one.
const (
	uniqueViolation     = "23505"
	foreignKeyViolation = "23503"
)

func refusedWith(err error, code string) bool {
	var refused *pgconn.PgError
	return errors.As(err, &refused) && refused.Code == code
}

func isDuplicate(err error) bool { return refusedWith(err, uniqueViolation) }

// Rules is the account's rules, newest first.
func (s Store) Rules(ctx context.Context, userID string) ([]Rule, error) {
	rows, err := New(s.Pool).RulesOfOwner(ctx, userID)
	if err != nil {
		return nil, err
	}
	rules := make([]Rule, len(rows))
	for i, row := range rows {
		rules[i] = Rule(row)
	}
	return rules, nil
}

// Create writes a rule for the account. A thread the rule names must be the
// account's own (ErrNoThread); a rule the account already has is
// ErrDuplicate.
//
// The thread is checked and the rule written in one transaction, so a
// thread deleted between the two cannot leave a rule scoped to nothing: the
// insert's foreign key refuses it, and that is ErrNoThread too.
func (s Store) Create(ctx context.Context, userID string, requested Requested, permission, pattern string) (Rule, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Rule{}, err
	}
	// After a commit this does nothing.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	queries := New(tx)
	if requested.ThreadID != nil {
		owned, err := queries.ThreadIsOwners(ctx, ThreadIsOwnersParams{ThreadID: *requested.ThreadID, UserID: userID})
		if err != nil {
			return Rule{}, err
		}
		if !owned {
			return Rule{}, ErrNoThread
		}
	}
	row, err := queries.InsertRule(ctx, InsertRuleParams{
		UserID: userID, ThreadID: requested.ThreadID, Permission: permission, Pattern: pattern, Action: requested.Action,
	})
	if err != nil {
		switch {
		case isDuplicate(err):
			return Rule{}, ErrDuplicate
		case requested.ThreadID != nil && refusedWith(err, foreignKeyViolation):
			// The thread was deleted after it was checked.
			return Rule{}, ErrNoThread
		}
		return Rule{}, err
	}
	return Rule(row), tx.Commit(ctx)
}

// Update changes the pattern, the action or both of one of the account's
// rules. found is false when the account has no such rule; a change that
// would make the rule the same as another of the account's is ErrDuplicate.
func (s Store) Update(ctx context.Context, userID string, ruleID int64, changes Changes) (rule Rule, found bool, err error) {
	params := UpdateRuleParams{ID: ruleID, UserID: userID}
	if changes.Pattern != nil {
		params.SetPattern, params.Pattern = true, StoredPattern(*changes.Pattern)
	}
	if changes.Action != nil {
		params.SetAction, params.Action = true, *changes.Action
	}
	row, err := New(s.Pool).UpdateRule(ctx, params)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Rule{}, false, nil
	case isDuplicate(err):
		return Rule{}, false, ErrDuplicate
	case err != nil:
		return Rule{}, false, err
	}
	return Rule(row), true, nil
}

// Delete removes one of the account's rules, and reports whether it had it.
func (s Store) Delete(ctx context.Context, userID string, ruleID int64) (bool, error) {
	removed, err := New(s.Pool).DeleteRule(ctx, DeleteRuleParams{ID: ruleID, UserID: userID})
	return removed > 0, err
}
