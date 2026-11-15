package automations

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// A chat draft becomes an automation through the create route, once, for
// its owner only; and the chat's check of a draft is the route's own.
func TestADraftIsCreatedOnceByItsOwner(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 8, 0, 0, 0, time.UTC)
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	routes := Routes{Pool: pool, Queue: &published{}, Models: menu{offered: true, listed: []string{"listed-model"}}, Errors: errorenvelope.Writer{},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now }}
	routes.Register(api)
	h := harness{t: t, api: api}
	draft := func(owner string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO automation_drafts (user_id, request) VALUES ($1, '{}') RETURNING id`, owner).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	state := func(id string) (status string, automation *int64) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT status, automation_id FROM automation_drafts WHERE id = $1`, id).Scan(&status, &automation); err != nil {
			t.Fatal(err)
		}
		return status, automation
	}
	count := func() (n int) {
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM automations`).Scan(&n)
		return n
	}
	withDraft := func(id string) string {
		return strings.TrimSuffix(digest, "}") + `,"draft_id":"` + id + `"}`
	}

	// Bob cannot create from Alice's draft, nor name one that was never made.
	alices := draft(alice)
	for name, body := range map[string]string{
		"another's draft": withDraft(alices),
		"not an id":       withDraft("drop table"),
		"never made":      withDraft("99999999-9999-4999-8999-999999999999"),
	} {
		if status, body := h.call(bob, "POST", ListPath, body); status != http.StatusNotFound || !strings.Contains(body, draftAbsent) {
			t.Errorf("%s: %d %s", name, status, body)
		}
	}
	// An invalid edit is refused in words and leaves the draft waiting.
	invalid := strings.Replace(withDraft(alices), `"timezone":"UTC"`, `"timezone":"Mars/Olympus"`, 1)
	if status, body := h.call(alice, "POST", ListPath, invalid); status != http.StatusUnprocessableEntity || !strings.Contains(body, "Mars/Olympus") {
		t.Errorf("an invalid edit: %d %s", status, body)
	}
	if status, _ := state(alices); status != "pending" || count() != 0 {
		t.Fatalf("after refusals: draft %s, %d automations", status, count())
	}

	// Two presses of Create at once make one automation; the other is told
	// the draft was already created.
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i], _ = h.call(alice, "POST", ListPath, withDraft(alices))
		}(i)
	}
	wg.Wait()
	if !(codes[0] == http.StatusCreated && codes[1] == http.StatusConflict || codes[1] == http.StatusCreated && codes[0] == http.StatusConflict) {
		t.Fatalf("two Creates answered %v", codes)
	}
	status, automation := state(alices)
	if status != "created" || automation == nil || count() != 1 {
		t.Fatalf("draft %s → %v, %d automations", status, automation, count())
	}
	if code, body := h.call(alice, "POST", ListPath, withDraft(alices)); code != http.StatusConflict || !strings.Contains(body, draftUsed) {
		t.Errorf("a third Create: %d %s", code, body)
	}

	// The builder's create, with no draft, is unchanged.
	if code, _ := h.call(alice, "POST", ListPath, digest); code != http.StatusCreated {
		t.Errorf("a create without a draft: %d", code)
	}
	// Deleting the automation keeps the draft's "created" (without a link).
	if code, _ := h.call(alice, "DELETE", ListPath+"/"+strconv.FormatInt(*automation, 10), ""); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
	if status, automation := state(alices); status != "created" || automation != nil {
		t.Errorf("after the automation was deleted: %s, %v", status, automation)
	}

	// The chat's check is the route's own: the same problems, in the same
	// words, including what only this tier can vouch for.
	var bobsNotebook int64
	if err := pool.QueryRow(ctx, `INSERT INTO notebooks (user_id, title) VALUES ($1, 'Bob''s') RETURNING id`, bob).Scan(&bobsNotebook); err != nil {
		t.Fatal(err)
	}
	foreign := strings.Replace(digest, `"query":"Summarise what arrived."`, `"query":"Summarise what arrived.","save_to_notebook_id":`+strconv.FormatInt(bobsNotebook, 10), 1)
	foreign = strings.Replace(foreign, `"plan":`, `"models":{"chat":"gone-model"},"plan":`, 1)
	problems, err := routes.checker().Check(ctx, alice, []byte(foreign))
	if err != nil {
		t.Fatal(err)
	}
	_, answered := h.call(alice, "POST", ListPath, foreign)
	// The body is JSON; the problems are compared as written.
	routeSays := strings.ReplaceAll(answered, `\"`, `"`)
	for _, problem := range problems {
		if !strings.Contains(routeSays, problem) {
			t.Errorf("the route does not say the check's %q: %s", problem, routeSays)
		}
	}
	if len(problems) != 2 || !strings.Contains(problems[0]+problems[1], "is not one of yours") || !strings.Contains(problems[0]+problems[1], `"gone-model" is not offered`) {
		t.Errorf("problems = %v", problems)
	}
	if problems, err := routes.checker().Check(ctx, alice, []byte(digest)); err != nil || len(problems) != 0 {
		t.Errorf("a good draft: %v, %v", problems, err)
	}
	if problems, _ := routes.checker().Check(ctx, alice, []byte(withDraft(alices))); len(problems) != 1 || !strings.Contains(problems[0], "draft_id") {
		t.Errorf("a draft naming a draft: %v", problems)
	}
}
