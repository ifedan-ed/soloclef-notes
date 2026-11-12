package llm

import (
	"context"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/testdatabase"
)

// The order of an account's choices, and that one account's choice is never
// another's: studio first, then chat, then the deployment's model.
func TestTheStudioModelIsTheAccountsOwnChoice(t *testing.T) {
	pool := testdatabase.Open(t)
	choices := Choices{Pool: pool, Default: "site-chat"}
	nothing, chatOnly, both, studioOnly := testdatabase.Account(t, pool), testdatabase.Account(t, pool), testdatabase.Account(t, pool), testdatabase.Account(t, pool)
	testdatabase.Exec(t, pool, `INSERT INTO user_models (user_id, purpose, model) VALUES
		($1, 'chat', 'her-chat'),
		($2, 'chat', 'their-chat'), ($2, 'studio', 'their-studio'), ($2, 'quick', 'their-quick'),
		($3, 'studio', 'his-studio')`, chatOnly, both, studioOnly)

	for account, want := range map[string]string{
		nothing:    "site-chat",
		chatOnly:   "her-chat",
		both:       "their-studio",
		studioOnly: "his-studio",
	} {
		got, err := choices.StudioModel(context.Background(), account)
		if err != nil || got != want {
			t.Errorf("StudioModel(%s) = %q, %v, want %q", account, got, err, want)
		}
	}
	if _, err := (Choices{Pool: pool}).StudioModel(context.Background(), nothing); err == nil {
		t.Error("a model was answered with no deployment default")
	}
}
