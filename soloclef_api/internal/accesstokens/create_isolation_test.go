package accesstokens

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/routeanalytics"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// The create, against Postgres, with two accounts: a key is its maker's
// row, stored as its hash and prefix only, shown whole once, listed to its
// maker alone, found by the gate's own lookup as its maker's, and never made
// by an API key.
func TestAKeyIsItsMakersAndIsShownOnce(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	Routes{Pool: pool, Tokens: Store{Pool: pool}, Analytics: routeanalytics.Capture{Sink: discarded{}},
		Errors: errorenvelope.Writer{ReportURL: "https://notes.example/contact"}, Logger: logger,
		Now: func() time.Time { return now }}.Register(api)
	// The body reader the tier wraps every route with, so the route reads
	// the body as it does in the running tier.
	handler := requestbody.Middleware{
		Covers: requestbody.CoveringRoutes(http.MethodPost + " " + CreatePath),
	}.Wrap(api.Adapter())
	as := func(userID string, method principal.Method, verb, path, body string) (int, http.Header, string) {
		t.Helper()
		request := httptest.NewRequest(verb, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request = request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: userID, Method: method}))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Code, recorder.Header(), recorder.Body.String()
	}
	stored := func() int {
		var count int
		if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM personal_access_tokens`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}

	status, header, raw := as(alice, principal.MethodSession, http.MethodPost, CreatePath, `{"label": " Her laptop ", "expires_in_days": 30}`)
	if status != http.StatusOK || header.Get("Cache-Control") != "no-store" {
		t.Fatalf("her create answered %d (Cache-Control %q) %s", status, header.Get("Cache-Control"), raw)
	}
	var created struct {
		ID        int64   `json:"id"`
		Label     string  `json:"label"`
		Token     string  `json:"token"`
		Prefix    string  `json:"prefix"`
		ExpiresAt *string `json:"expires_at"`
	}
	if err := json.Unmarshal([]byte(raw), &created); err != nil {
		t.Fatal(err)
	}
	if created.Label != "Her laptop" || !strings.HasPrefix(created.Token, principal.TokenPrefix) ||
		created.Prefix != created.Token[:16] || created.ExpiresAt == nil || *created.ExpiresAt != "2026-12-12T10:00:00Z" {
		t.Fatalf("her new key: %+v", created)
	}

	// Stored under her id, as its hash and prefix, and the key itself in no
	// column of the row.
	var owner, hash, prefix, label, row string
	if err := pool.QueryRow(t.Context(), `SELECT user_id::text, token_hash, token_prefix, label, t::text
		FROM personal_access_tokens t WHERE id = $1`, created.ID).Scan(&owner, &hash, &prefix, &label, &row); err != nil {
		t.Fatal(err)
	}
	if owner != alice || hash != principal.HashToken(created.Token) || prefix != created.Prefix || label != "Her laptop" {
		t.Errorf("her row: owner %s, hash %s, prefix %s, label %q", owner, hash, prefix, label)
	}
	if strings.Contains(row, created.Token) {
		t.Errorf("the key itself is stored: %s", row)
	}
	// The gate finds the key as hers.
	found, ok, err := principal.PostgresDirectory{Pool: pool, Logger: logger}.PersonalAccessTokenOwner(t.Context(), principal.HashToken(created.Token), now)
	if err != nil || !ok || found.UserID != alice || found.TokenID != created.ID {
		t.Errorf("the gate's lookup of her key: %+v %v %v", found, ok, err)
	}

	// Shown once: the list has her key's prefix and never the key.
	status, _, list := as(alice, principal.MethodSession, http.MethodGet, ListPath, "")
	if status != http.StatusOK || !strings.Contains(list, created.Prefix) || strings.Contains(list, created.Token) {
		t.Errorf("her list answered %d %s", status, list)
	}
	// Not his: his list is empty, and his own key is his.
	if status, _, his := as(bob, principal.MethodSession, http.MethodGet, ListPath, ""); status != http.StatusOK || his != "[]" {
		t.Errorf("his list answered %d %s", status, his)
	}
	if status, _, raw := as(bob, principal.MethodSession, http.MethodPost, CreatePath, `{"label": "His script"}`); status != http.StatusOK || !strings.Contains(raw, `"expires_at":null`) {
		t.Fatalf("his create answered %d %s", status, raw)
	}
	if status, _, his := as(bob, principal.MethodSession, http.MethodGet, ListPath, ""); status != http.StatusOK || !strings.Contains(his, "His script") || strings.Contains(his, "Her laptop") {
		t.Errorf("his list answered %d %s", status, his)
	}

	// A key cannot make a key, whatever its body; nor is a bad body stored.
	for _, body := range []string{`{"label": "successor"}`, `{"label": ""}`} {
		if status, _, raw := as(alice, principal.MethodPersonalAccessToken, http.MethodPost, CreatePath, body); status != http.StatusForbidden {
			t.Errorf("a key's create of %s answered %d %s", body, status, raw)
		}
	}
	if status, _, raw := as(alice, principal.MethodSession, http.MethodPost, CreatePath, `{"label": "  "}`); status != http.StatusUnprocessableEntity {
		t.Errorf("a blank name answered %d %s", status, raw)
	}
	if stored() != 2 {
		t.Errorf("%d keys are stored, want hers and his", stored())
	}
}
