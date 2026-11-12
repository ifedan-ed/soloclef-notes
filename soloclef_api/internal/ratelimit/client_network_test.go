package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Anyone with an IPv6 network -- every VPS is handed a /64 -- can send each
// request from a different address in it. Counted by address, each one would
// get a fresh budget and the ten-a-minute password limit would limit nothing.
// The network is the client.
func TestOneIPv6NetworkSharesOneBudget(t *testing.T) {
	counter := &MemoryCounter{}
	limiter := Middleware{
		Counter: counter,
		Limit:   Limit{Amount: 3, Multiples: 1, Granularity: "minute", Window: time.Minute},
		Covers:  CoveringRoutes("POST /auth/jwt/login"),
		Errors:  testErrors(),
		Logger:  quietLogger(),
	}
	handler := limiter.Wrap(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusOK)
	}))

	ask := func(address string) int {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/auth/jwt/login", nil)
		request.Header.Set("X-Real-IP", address)
		handler.ServeHTTP(recorder, request)
		return recorder.Code
	}
	// Four addresses of one /64, each asking once.
	rotated := []string{"2001:db8:1:2::1", "2001:db8:1:2::2", "2001:db8:1:2:ffff::9", "2001:db8:1:2:abcd:1:2:3"}
	statuses := []int{}
	for _, address := range rotated {
		statuses = append(statuses, ask(address))
	}
	if want := []int{200, 200, 200, 429}; statuses[3] != want[3] {
		t.Fatalf("rotating within one /64 answered %v, want %v", statuses, want)
	}
	// Another network is another client.
	if got := ask("2001:db8:1:3::1"); got != http.StatusOK {
		t.Errorf("the neighbouring /64 answered %d, want 200", got)
	}
}

func TestWhoARequestIsCountedAs(t *testing.T) {
	cases := map[string]string{
		"198.51.100.9":            "198.51.100.9",
		"2001:db8:1:2:abcd::1":    "2001:db8:1:2::/64",
		"2001:DB8:1:2::7":         "2001:db8:1:2::/64",
		"::ffff:198.51.100.9":     "198.51.100.9",
		"fe80::1%eth0":            "fe80::/64",
		"not an address":          "not an address",
		"":                        "",
		"2001:db8:1:2:abcd::1/48": "2001:db8:1:2:abcd::1/48",
	}
	for given, want := range cases {
		if got := countedAs(given); got != want {
			t.Errorf("countedAs(%q) = %q, want %q", given, got, want)
		}
	}
}
