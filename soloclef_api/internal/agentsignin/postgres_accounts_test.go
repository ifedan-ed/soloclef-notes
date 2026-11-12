package agentsignin

import (
	"context"
	"errors"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres for this package's database
// test. **Every account in it is deleted** (internal/testpostgres), so it
// must never name a database anyone uses. With it unset the test is skipped.
const databaseVariable = "SOLOCLEF_API_TEST_AGENTSIGNIN_DATABASE"

// An agent's token is matched to an account two ways, by the identity's link
// and by a verified address, and each way must hand the gate that account's
// own API-access switch: the switch of the account found, and nobody else's.
func TestAnAgentIsHandedItsOwnAccountsAPIAccessSwitch(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	ctx := context.Background()
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	// Alice signed in on the web through the provider, so her identity is
	// linked, and she has API access on. Bob is found by his verified
	// address, and has left it off.
	testpostgres.Exec(t, pool, `
UPDATE "user" SET api_access_enabled = true WHERE id = '`+alice+`';
UPDATE "user" SET is_verified = true WHERE id = '`+bob+`';
INSERT INTO oauth_account (user_id, oauth_name, account_id, account_email)
VALUES ('`+alice+`', 'provider', 'subject-alice', 'alice@example.test')`)
	accounts := PostgresAccounts{Pool: pool, ProviderName: "provider"}

	linked, err := accounts.Account(ctx, Identity{Subject: "subject-alice"})
	if err != nil || linked.UserID != alice || !linked.APIAccessEnabled {
		t.Errorf("the linked account is %+v, %v; want hers with API access on", linked, err)
	}
	byAddress, err := accounts.Account(ctx, Identity{Subject: "subject-bob", Email: "bob@example.test", EmailVerified: true})
	if err != nil || byAddress.UserID != bob || byAddress.APIAccessEnabled {
		t.Errorf("the account found by address is %+v, %v; want his with API access off", byAddress, err)
	}
	// A deactivated account is no account, whatever its switch says.
	testpostgres.Exec(t, pool, `UPDATE "user" SET is_active = false WHERE id = '`+alice+`'`)
	if _, err := accounts.Account(ctx, Identity{Subject: "subject-alice"}); !errors.Is(err, ErrNoAccount) {
		t.Errorf("a deactivated account resolved: %v", err)
	}
}
