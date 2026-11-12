package websearch

import (
	"context"
	"errors"
	"fmt"
)

// Why a provider answered no results, for a caller that words the reason
// itself (the notebook research box names each in the reader's language).
// The tools do not need them: a model is given the sentence.
var (
	// ErrNotConnected: the account has no key for the provider.
	ErrNotConnected = errors.New("websearch: the provider is not connected")
	// ErrKeyRejected: the provider refused the account's key.
	ErrKeyRejected = errors.New("websearch: the provider rejected the key")
	// ErrQuota: the account's plan with the provider is spent or rate limited.
	ErrQuota = errors.New("websearch: the provider account is out of quota")
)

// refusal is a provider's refusal: the sentence the model reads, and which
// of the reasons above it is, so the sentence did not have to change for the
// reason to become readable by code.
type refusal struct {
	sentence string
	reason   error
}

func (r *refusal) Error() string { return r.sentence }
func (r *refusal) Unwrap() error { return r.reason }

// Find is one search for an account outside a chat turn: the notebook
// research box. It is the tool's own path (the key read at the moment of the
// call, one request, no retry), so the box and the chat cannot search a
// provider two different ways. limit is held to what one request returns.
func (s Search) Find(ctx context.Context, userID string, provider Provider, query string, limit int) ([]Result, error) {
	if provider != Exa && provider != Tavily {
		return nil, fmt.Errorf("websearch: unknown provider %q", provider)
	}
	key, found, err := s.Keys.Key(ctx, userID, provider)
	if err != nil {
		return nil, fmt.Errorf("websearch: reading the account's %s key: %w", provider.label(), err)
	}
	if !found {
		return nil, ErrNotConnected
	}
	return s.search(ctx, provider, key, query, min(max(limit, 1), maxResults))
}
