// Package callercredential carries the caller's credential for the life of
// one request.
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

const credentialKey contextKey = 0

// Bind attaches the caller's credential to a request's context.
func Bind(ctx context.Context, credential string) context.Context {
	return context.WithValue(ctx, credentialKey, credential)
}

// Credential is the caller's credential for the in-flight request, or empty
// under stdio.
func Credential(ctx context.Context) string {
	credential, _ := ctx.Value(credentialKey).(string)
	return credential
}

// Identity is the stable per-caller key that request state is scoped by: the
// API key itself, which does not change while it is in use. It is shared
// under stdio, where there is one caller.
func Identity(ctx context.Context) string {
	if credential := Credential(ctx); credential != "" {
		return credential
	}
	return LocalIdentity
}
