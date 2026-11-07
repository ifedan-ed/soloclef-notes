package principal

import "context"

type contextKey struct{}

// WithPrincipal carries the resolved caller to the handler.
func WithPrincipal(ctx context.Context, caller Principal) context.Context {
	return context.WithValue(ctx, contextKey{}, caller)
}

// FromContext is the caller a handler is serving. A handler only runs behind
// the gate, so the second result being false means the gate was bypassed and
// the handler must refuse rather than guess.
func FromContext(ctx context.Context) (Principal, bool) {
	caller, ok := ctx.Value(contextKey{}).(Principal)
	return caller, ok
}
