// Package requestcontext gives every request an id and one access line, as the
// Python tier's RequestContextMiddleware does, so a request served by Go can be
// followed through the same logs and the same X-Request-ID header.
package requestcontext

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"regexp"
)

// Header is app.observability.logs.access.REQUEST_ID_HEADER.
const Header = "X-Request-ID"

// wellFormed is that module's _VALID_REQUEST_ID: a caller's own id is kept only
// while it is short and plain.
var wellFormed = regexp.MustCompile(`^[A-Za-z0-9._:-]{6,64}$`)

type contextKey struct{}

// Accept returns the caller's id when it is usable, and mints one otherwise.
func Accept(raw string) string {
	if wellFormed.MatchString(raw) {
		return raw
	}
	return mint()
}

func mint() string {
	// req_<12 hex>, as new_request_id() writes it.
	buffer := make([]byte, 6)
	if _, err := rand.Read(buffer); err != nil {
		return "req_000000000000"
	}
	return "req_" + hex.EncodeToString(buffer)
}

// WithID carries the id through to the handler.
func WithID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, contextKey{}, requestID)
}

// ID is the request's id, or "" outside a request.
func ID(ctx context.Context) string {
	id, _ := ctx.Value(contextKey{}).(string)
	return id
}

// IDFrom reads the id a request is carrying.
func IDFrom(request *http.Request) string {
	return ID(request.Context())
}
