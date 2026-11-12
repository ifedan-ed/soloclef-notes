package llm

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
)

// Choices answers which model an account's long written work (a podcast's
// transcript, a video's script) is drafted by, as the stored name: a proxy
// name, or "own:<n>" for a model of the account's own connection. In order:
//
//  1. the account's studio model, if it chose one;
//  2. the account's chat model, if it chose one;
//  3. the deployment's chat model.
//
// This is the API tier's rule for the studio purpose
// (soloclef_api/internal/modelchoices), where the settings screen shows the
// same three steps: a studio choice, else "the model answering your chat".
// It is an order of the account's own choices and not a retry list: the one
// name it answers is the model that drafts, and if that model fails the job
// fails and says so.
type Choices struct {
	Pool *pgxpool.Pool
	// Default is the proxy's name for the deployment's chat model, the API
	// tier's CHAT_MODEL.
	Default string
}

// StudioModel is the model name for the account.
func (c Choices) StudioModel(ctx context.Context, userID string) (string, error) {
	if c.Default == "" {
		return "", errors.New("llm: no default chat model")
	}
	// Both rows are read in one statement and the studio one sorts first.
	var chosen string
	err := c.Pool.QueryRow(ctx,
		`SELECT model FROM user_models
		 WHERE user_id = $1::uuid AND purpose IN ('studio', 'chat')
		 ORDER BY purpose = 'studio' DESC
		 LIMIT 1`, userID).Scan(&chosen)
	if errors.Is(err, pgx.ErrNoRows) {
		return c.Default, nil
	}
	if err != nil {
		return "", fmt.Errorf("llm: reading the account's studio model: %w", err)
	}
	return chosen, nil
}

// Writers is a model for an account's long work: the account's studio
// model, with its reasoning left on. A proxy name is called through the
// proxy; a model of the account's own OpenAI-compatible connection
// ("own:<n>") at the address the person gave, through the egress guard.
// Which of the two a name is, ownmodels.Resolver says, as it does for the
// chat.
type Writers struct {
	Choices Choices
	Proxy   Proxy
	// Own opens a model of an account's own connection
	// (ownmodels.Connections).
	Own ownmodels.Opener
}

// Check reports what is missing. Called once at start-up.
func (w Writers) Check() error {
	if w.Own == nil {
		return errors.New("llm: the account's own model connections are not set")
	}
	return w.Proxy.Check()
}

// StudioModel is Choices.StudioModel.
func (w Writers) StudioModel(ctx context.Context, userID string) (string, error) {
	return w.Choices.StudioModel(ctx, userID)
}

// Client is a client for the named model, for the account whose choice it
// is. An own model that cannot be called is an error carrying the sentence
// for it (ownmodels.Unavailable); no other model drafts instead.
func (w Writers) Client(ctx context.Context, userID, name string) (model.Model, error) {
	resolver := ownmodels.Resolver{Proxy: func(name string) (model.Model, error) { return w.Proxy.Client(name, false) }, Own: w.Own}
	return resolver.Open(ctx, userID, name)
}
