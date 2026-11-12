package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The order is the whole point: a tier that read a different header would count
// a different client, and then neither tier's cap would mean anything.
func TestTheClientIsChosenInGetRealClientIpsOrder(t *testing.T) {
	cases := map[string]struct {
		headers map[string]string
		peer    string
		want    string
	}{
		"the Cloudflare header wins": {
			map[string]string{
				"CF-Connecting-IP": "203.0.113.7",
				"X-Real-IP":        "198.51.100.9",
				"X-Forwarded-For":  "192.0.2.1, 10.0.0.1",
			}, "10.1.2.3:4567", "203.0.113.7",
		},
		"a forwarded-for spoofed behind a real Cloudflare header is ignored": {
			map[string]string{
				"CF-Connecting-IP": "203.0.113.7",
				"X-Forwarded-For":  "1.1.1.1",
			}, "10.1.2.3:4567", "203.0.113.7",
		},
		"the real-ip header is next": {
			map[string]string{
				"X-Real-IP":       "198.51.100.9",
				"X-Forwarded-For": "192.0.2.1, 10.0.0.1",
			}, "10.1.2.3:4567", "198.51.100.9",
		},
		"the first entry of forwarded-for is next": {
			map[string]string{"X-Forwarded-For": " 192.0.2.1 , 10.0.0.1 "},
			"10.1.2.3:4567", "192.0.2.1",
		},
		"the socket peer is last":       {nil, "10.1.2.3:4567", "10.1.2.3"},
		"a request with no peer at all": {nil, "", "127.0.0.1"},
		// A header that is present but holds only spaces is chosen there and
		// stripped to nothing, and the limiter then skips the request. This
		// tier must skip it too, or it would refuse requests the Python tier
		// lets through.
		"a whitespace-only header names nobody": {
			map[string]string{"CF-Connecting-IP": "   "}, "10.1.2.3:4567", "",
		},
		"a forwarded-for whose first entry is empty names nobody": {
			map[string]string{"X-Forwarded-For": " , 192.0.2.1"}, "10.1.2.3:4567", "",
		},
	}

	for name, test := range cases {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/notebooks/1/notes", nil)
		request.RemoteAddr = test.peer
		for header, value := range test.headers {
			request.Header.Set(header, value)
		}
		if got := ClientIP(request); got != test.want {
			t.Errorf("%s: got %q, want %q", name, got, test.want)
		}
	}
}
