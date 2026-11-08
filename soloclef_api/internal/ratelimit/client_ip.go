package ratelimit

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP is app.rate_limiter.get_real_client_ip, in its order:
// CF-Connecting-IP, then X-Real-IP, then the first entry of X-Forwarded-For,
// then the socket peer.
//
// It returns what that function returns, including the empty string: a header
// that is present but holds only whitespace is chosen there and then stripped to
// nothing, and the limiter skips a request whose client it cannot name
// (__evaluate_limits: "Empty value found in parameters"). Spelling that case out
// here is the difference between mirroring the limiter and writing a stricter
// one that refuses requests the Python tier lets through.
func ClientIP(request *http.Request) string {
	if value := request.Header.Get("CF-Connecting-IP"); value != "" {
		return strings.TrimSpace(value)
	}
	if value := request.Header.Get("X-Real-IP"); value != "" {
		return strings.TrimSpace(value)
	}
	if value := request.Header.Get("X-Forwarded-For"); value != "" {
		first, _, _ := strings.Cut(value, ",")
		return strings.TrimSpace(first)
	}
	return peerAddress(request)
}

// peerAddress is `request.client.host if request.client else "127.0.0.1"`.
func peerAddress(request *http.Request) string {
	if request.RemoteAddr == "" {
		return "127.0.0.1"
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return request.RemoteAddr
	}
	return host
}
