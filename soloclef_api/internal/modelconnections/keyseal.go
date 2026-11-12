package modelconnections

import "git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"

// KeySeal encrypts a provider key before it is stored and opens it for the
// things that use it: a test, a listing of the endpoint's models, and a
// call to a model of an account's own connection.
//
// It is ownmodels.KeySeal (read that for the format and why the owner is
// bound in), because the worker opens what this tier seals: a podcast, a
// video or an automation that runs on an account's own model opens its key
// there. One implementation is one format.
type KeySeal = ownmodels.KeySeal

// ErrSealUnreadable is a stored key this deployment's secret did not seal,
// or a damaged one.
var ErrSealUnreadable = ownmodels.ErrSealUnreadable

// NewKeySeal derives the sealing key from the deployment's secret.
func NewKeySeal(secret string) (KeySeal, error) { return ownmodels.NewKeySeal(secret) }
