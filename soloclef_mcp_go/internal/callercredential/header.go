package callercredential

import (
	"net/http"
	"strings"
)

const bearerPrefix = "bearer "

// FromHeaders returns the caller's credential from the Authorization: Bearer
// slot the backend already expects, falling back to X-API-Key for clients that
// can only send a custom header.
func FromHeaders(headers http.Header) string {
	authorization := headers.Get("Authorization")
	if len(authorization) >= len(bearerPrefix) &&
		strings.EqualFold(authorization[:len(bearerPrefix)], bearerPrefix) {
		if token := strings.TrimSpace(authorization[len(bearerPrefix):]); token != "" {
			return token
		}
	}
	return strings.TrimSpace(headers.Get("X-API-Key"))
}
