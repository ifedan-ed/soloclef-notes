package principal

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// directoryVariable names a throwaway Postgres for this test. **Every account
// in it is deleted** (internal/testpostgres), so it must never name a
// database anyone uses. With it unset the test is skipped.
const directoryVariable = "SOLOCLEF_API_TEST_PRINCIPAL_DATABASE"

// The account's API-access switch, read from the real column by the real
// statement: a key works only while its account has the switch on, the
// switch is off for a new account, and turning it on for one account turns
// it on for no other.
func TestTheAPIAccessSwitchIsReadWithTheKey(t *testing.T) {
	pool := testpostgres.Open(t, directoryVariable)
	ctx := context.Background()
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	aliceKey, bobKey := TokenPrefix+"alice-key", TokenPrefix+"bob-key"
	testpostgres.Exec(t, pool, `
INSERT INTO personal_access_tokens (user_id, token_hash, token_prefix, label) VALUES
 ('`+alice+`', '`+HashToken(aliceKey)+`', 'ss_pat_alic', 'Hers'),
 ('`+bob+`',   '`+HashToken(bobKey)+`',   'ss_pat_bob',  'His')`)
	resolver := Resolver{
		SessionTokens:     SessionToken{Secret: testSecret},
		Directory:         PostgresDirectory{Pool: pool, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))},
		SessionCookieName: "surfsense_session",
		Now:               time.Now,
	}
	with := func(key string) (Principal, error) {
		request, _ := http.NewRequest(http.MethodGet, "/api/v1/notebooks", nil)
		request.Header.Set("Authorization", "Bearer "+key)
		return resolver.Resolve(ctx, request)
	}
	refused := func(err error) bool {
		var refusal Refusal
		return errors.As(err, &refusal) && refusal == ErrAPIAccessOff
	}

	// Off by default: both keys are valid, and neither works.
	for _, key := range []string{aliceKey, bobKey} {
		if _, err := with(key); !refused(err) {
			t.Fatalf("a key of an account with API access off resolved: %v", err)
		}
	}
	// She turns hers on. Her key works; his still does not.
	if _, err := pool.Exec(ctx, `UPDATE "user" SET api_access_enabled = true WHERE id = $1`, alice); err != nil {
		t.Fatal(err)
	}
	caller, err := with(aliceKey)
	if err != nil || caller.UserID != alice || caller.Method != MethodPersonalAccessToken {
		t.Fatalf("her key resolved to %+v, %v", caller, err)
	}
	if _, err := with(bobKey); !refused(err) {
		t.Errorf("his key works because she turned hers on: %v", err)
	}
	// And off again ends it at once: the switch is read on every request.
	if _, err := pool.Exec(ctx, `UPDATE "user" SET api_access_enabled = false WHERE id = $1`, alice); err != nil {
		t.Fatal(err)
	}
	if _, err := with(aliceKey); !refused(err) {
		t.Errorf("her key still works after she turned access off: %v", err)
	}
}

// The gate's sign-in check, read by the real statement from the real rows:
// a sign-in is live while one of its tokens is unspent and unexpired, only
// for the account it belongs to, and only while that account is active.
func TestASignInIsLiveOnlyWhileItHasAnUnspentTokenOfItsOwnActiveAccount(t *testing.T) {
	pool := testpostgres.Open(t, directoryVariable)
	ctx := context.Background()
	directory := PostgresDirectory{Pool: pool, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	const (
		rotated   = "aaaaaaaa-0000-4000-8000-000000000001" // spent rows and one live successor
		ended     = "aaaaaaaa-0000-4000-8000-000000000002" // signed out: every row spent
		idle      = "aaaaaaaa-0000-4000-8000-000000000003" // unspent, but past its expiry
		capped    = "aaaaaaaa-0000-4000-8000-000000000004" // unspent, but past its sign-in's cap
		his       = "aaaaaaaa-0000-4000-8000-000000000005" // Bob's live sign-in
		neverMade = "aaaaaaaa-0000-4000-8000-000000000006"
	)
	testpostgres.Exec(t, pool, `
INSERT INTO refresh_tokens (user_id, token_hash, expires_at, revoked_at, absolute_expiry, family_id, kind) VALUES
 ('`+alice+`', 'r1', '2026-11-20', '2026-11-12 08:00', '2026-12-01', '`+rotated+`', 'web'),
 ('`+alice+`', 'r2', '2026-11-20', '2026-11-12 08:30', '2026-12-01', '`+rotated+`', 'web'),
 ('`+alice+`', 'r3', '2026-11-26', NULL,               '2026-12-01', '`+rotated+`', 'web'),
 ('`+alice+`', 'e1', '2026-11-12 08:59', '2026-11-12 08:59', '2027-11-12', '`+ended+`', 'app'),
 ('`+alice+`', 'i1', '2026-11-12 08:00', NULL, '2026-12-01', '`+idle+`', 'web'),
 ('`+alice+`', 'c1', '2026-11-20', NULL, '2026-11-12 08:00', '`+capped+`', 'web'),
 ('`+bob+`',   'b1', '2026-11-20', NULL, '2026-12-01', '`+his+`', 'web')`)

	for _, c := range []struct {
		why          string
		user, signIn string
		live         bool
	}{
		{"her rotated sign-in", alice, rotated, true},
		{"her signed-out sign-in", alice, ended, false},
		{"her sign-in left past its idle time", alice, idle, false},
		{"her sign-in past its cap", alice, capped, false},
		{"a sign-in never made", alice, neverMade, false},
		{"his sign-in, asked as hers", alice, his, false},
		{"her sign-in, asked as his", bob, rotated, false},
		{"his own sign-in", bob, his, true},
	} {
		live, err := directory.SessionIsLive(ctx, c.user, c.signIn, now)
		if err != nil || live != c.live {
			t.Errorf("%s: live = %v, %v; want %v", c.why, live, err, c.live)
		}
	}

	// A deactivated account has no live sign-in, whatever its rows say.
	testpostgres.Exec(t, pool, `UPDATE "user" SET is_active = false WHERE id = '`+alice+`'`)
	if live, err := directory.SessionIsLive(ctx, alice, rotated, now); err != nil || live {
		t.Errorf("a deactivated account's sign-in is live = %v, %v", live, err)
	}
	// And a deleted one neither: no row, the same answer, not an error.
	testpostgres.Exec(t, pool, `DELETE FROM "user" WHERE id = '`+bob+`'`)
	if live, err := directory.SessionIsLive(ctx, bob, his, now); err != nil || live {
		t.Errorf("a deleted account's sign-in is live = %v, %v", live, err)
	}
}

// The check is one probe of the partial index on unspent tokens, however
// many spent rows a sign-in has piled up: a phone refreshing hourly for a
// year has thousands.
func TestTheSignInCheckReadsTheIndexOfUnspentTokens(t *testing.T) {
	pool := testpostgres.Open(t, directoryVariable)
	const family = "bbbbbbbb-0000-4000-8000-000000000001"
	testpostgres.Exec(t, pool, `
INSERT INTO refresh_tokens (user_id, token_hash, expires_at, revoked_at, family_id, kind)
SELECT '`+testpostgres.Alice+`', 'spent-' || n, '2027-11-12', '2026-11-12'::timestamptz - n * interval '1 hour', '`+family+`', 'app'
FROM generate_series(1, 9000) AS n;
INSERT INTO refresh_tokens (user_id, token_hash, expires_at, family_id, kind)
VALUES ('`+testpostgres.Alice+`', 'live', '2027-11-12', '`+family+`', 'app');
ANALYZE refresh_tokens;`)
	rows, err := pool.Query(context.Background(), `EXPLAIN (FORMAT TEXT) `+sessionIsLiveSQL,
		testpostgres.Alice, family, time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if text := strings.Join(plan, "\n"); !strings.Contains(text, "ix_refresh_tokens_live_family") {
		t.Errorf("the check does not use the index of unspent tokens:\n%s", text)
	}
}
