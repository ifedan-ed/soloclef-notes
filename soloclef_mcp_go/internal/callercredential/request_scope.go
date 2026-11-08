// Package callercredential carries the caller's credential and identity for the
// life of one request.
//
// Over streamable HTTP one process serves many users, so the credential cannot
// be a field on a shared client: it is bound to the request's context by the
// transport and read there by the REST client, which sends it on to the
// backend, the one judge of it. Under stdio there is no request, so nothing is
// bound and the environment's key is used instead.
package callercredential

import "context"

// LocalIdentity is the identity of a caller with no credential of its own,
// which is every call made over stdio.
const LocalIdentity = "__local__"

type contextKey int

const (
	credentialKey contextKey = iota
	identityKey
)

// Bind attaches the caller's credential and identity to a request's context.
//
// An API key is its own identity. An agent's sign-in token is replaced every
// hour when it refreshes, so it is bound with the backend's stable
// "user:<id>" instead, or the choice it made would be lost each refresh.
func Bind(ctx context.Context, credential, identity string) context.Context {
	ctx = context.WithValue(ctx, credentialKey, credential)
	return context.WithValue(ctx, identityKey, identity)
}

// Credential is the caller's credential for the in-flight request, or empty
// under stdio.
func Credential(ctx context.Context) string {
	credential, _ := ctx.Value(credentialKey).(string)
	return credential
}

// Identity is the stable per-caller key that request state is scoped by; it is
// shared under stdio, where there is one caller.
func Identity(ctx context.Context) string {
	if identity, _ := ctx.Value(identityKey).(string); identity != "" {
		return identity
	}
	if credential := Credential(ctx); credential != "" {
		return credential
	}
	return LocalIdentity
}
