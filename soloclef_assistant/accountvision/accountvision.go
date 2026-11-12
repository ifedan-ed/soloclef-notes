// Package accountvision is the vision model that looks at an account's
// pictures, opened: the one rule every caller that shows a vision model a
// picture for an account follows, so that an upload's pictures, a mail
// attachment, a Drive or Nextcloud file, a built document's pages, an
// infographic and a video's stills are all looked at by the same model.
//
// # Which model
//
// The account's own vision choice (`user_models`, purpose vision, set at
// /users/me/model-roles), else the deployment's model for the job, else
// none (ErrNone), which each caller says in its own words. Nothing else is
// tried: a choice that cannot be called is said so, and the deployment's
// model is not used in its place, because the account chose and a
// description by a model it did not choose is a wrong answer that looks
// right.
//
// The choice is a proxy name or a model of the account's own
// OpenAI-compatible connection ("own:<id>"), opened by
// ownmodels.VisionResolver: through the proxy, or at the person's address
// with their key, their headers and the egress guard.
package accountvision

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/vision"
)

// ErrNone is an account with no vision model: it chose none, and the
// deployment has none for the job.
var ErrNone = errors.New("accountvision: no vision model is set for this account or on this server")

// Choices is where an account's vision choice is read.
type Choices interface {
	// VisionChoice is the account's chosen name, or "" for none.
	VisionChoice(ctx context.Context, userID string) (string, error)
}

// Store is Choices over the pool.
type Store struct {
	Pool *pgxpool.Pool
}

func (s Store) VisionChoice(ctx context.Context, userID string) (string, error) {
	model, err := New(s.Pool).VisionChoiceOfAccount(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return model, err
}

// Opener opens a vision choice for an account: ownmodels.VisionResolver.
type Opener interface {
	Open(ctx context.Context, userID, name string) (vision.Endpoint, error)
}

// Name is the vision model the rule picks: the account's choice, else the
// deployment's, "" for none. The indexer, which reads the choice with the
// document it indexes, asks this directly.
func Name(chosen, deployment string) string {
	if chosen != "" {
		return chosen
	}
	return deployment
}

// Models opens the vision model for an account's job.
type Models struct {
	Choices Choices
	// Default is the deployment's vision model for this job, "" for none.
	Default string
	Open    Opener
}

// Check reports what is missing. Called once at start-up.
func (m Models) Check() error {
	if m.Choices == nil || m.Open == nil {
		return errors.New("accountvision: the account choices and the opener are both required")
	}
	return nil
}

// Chosen is the name of the account's vision model, "" for none, without
// opening it: what a caller asks to know whether there is one at all.
func (m Models) Chosen(ctx context.Context, userID string) (string, error) {
	chosen, err := m.Choices.VisionChoice(ctx, userID)
	if err != nil {
		// Not the deployment's model in its place, for the reason the
		// package gives: the account may have chosen.
		return "", fmt.Errorf("accountvision: the account's vision model could not be read: %w", err)
	}
	return Name(chosen, m.Default), nil
}

// For is the account's vision model, opened. ErrNone when there is none;
// an *ownmodels.Unavailable, with the sentence for the person, when the
// chosen model of its own connection cannot be called.
func (m Models) For(ctx context.Context, userID string) (vision.Endpoint, error) {
	name, err := m.Chosen(ctx, userID)
	if err != nil {
		return vision.Endpoint{}, err
	}
	if name == "" {
		return vision.Endpoint{}, ErrNone
	}
	return m.Open.Open(ctx, userID, name)
}

// ForDeployment is Models as a server runs them: the choices in pool, the
// proxy's models through proxy, the account's own connections through own
// (ownmodels.ForDeployment), and the deployment's model for the job.
func ForDeployment(pool *pgxpool.Pool, proxy vision.Client, own ownmodels.VisionOpener, deployment string) (Models, error) {
	resolver := ownmodels.VisionResolver{Proxy: proxy, Own: own}
	if err := resolver.Check(); err != nil {
		return Models{}, err
	}
	models := Models{Choices: Store{Pool: pool}, Default: deployment, Open: resolver}
	return models, models.Check()
}

// Sentence is what a person is told when their vision model cannot be
// opened: the own connection's own sentence, or none (ok false) for any
// other failure, which the caller words as one of the moment.
func Sentence(err error) (string, bool) {
	var unavailable *ownmodels.Unavailable
	if errors.As(err, &unavailable) {
		return unavailable.Message, true
	}
	return "", false
}
