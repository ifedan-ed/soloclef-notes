// Package oidc verifies an ID token from the website's single sign-on
// provider: the provider's discovery document and signing keys, read and
// cached, and the checks a token must pass before any claim in it is
// believed.
//
// It is the provider-facing half of single sign-on, kept apart from the
// sign-in flow (internal/oidcsignin), which owns the redirects, the attempt
// cookie, the nonce and the account the identity becomes. That split keeps
// "is this token genuine" one small, separately tested decision.
//
// The rules (accepted algorithms, leeway, required claims, audience and
// issuer checks, the key-rotation refresh and its cooldown) were carried over
// from the Python tier exactly, because a difference in any of them either
// locks people out or lets a token in that was refused before.
package oidc

import "errors"

// ErrRefused means the token was read and is not a valid one from this
// provider for this client. The reason belongs in the log, never in a
// response body.
var ErrRefused = errors.New("the token is not a valid sign-in from the provider")

// ErrProviderUnavailable means the provider's metadata or keys could not be
// read, so the token was not judged either way. A caller answers it as "try
// again shortly", never as a bad token: the token may be perfectly good.
var ErrProviderUnavailable = errors.New("the identity provider could not be read")
