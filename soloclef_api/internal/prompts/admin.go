package prompts

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// MayPublish reports whether the caller may publish a prompt to every
// account: whether the account is an administrator, which is the account's
// own `is_superuser` flag and nothing else.
//
// There was a second way in, a list of addresses in a setting
// (BILLING_ADMIN_EMAILS). It is gone with billing: an address in a setting
// is a second place to look for who administers a deployment, it needed its
// own guard against an unverified account that merely claimed the address,
// and the web app never read it -- it shows the admin pages by the
// `is_superuser` that /users/me answers. One flag, on the row, that the
// operator sets (`soloclef-api set-admin`).
//
// It is read inside the write's transaction and only for a request that
// actually asks to publish, the only time the question matters.
func MayPublish(ctx context.Context, tx pgx.Tx, callerID string) (bool, error) {
	return New(tx).AccountIsAdministrator(ctx, callerID)
}
