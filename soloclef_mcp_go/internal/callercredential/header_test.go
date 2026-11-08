package callercredential_test

import (
	"context"
	"net/http"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/callercredential"
)

func TestTheBearerSlotIsReadFirstAndTheCustomHeaderIsTheFallback(t *testing.T) {
	cases := []struct {
		label  string
		set    map[string]string
		expect string
	}{
		{"bearer", map[string]string{"Authorization": "Bearer ss_pat_a"}, "ss_pat_a"},
		{"bearer, lower case scheme",
			map[string]string{"Authorization": "bearer ss_pat_a"}, "ss_pat_a"},
		{"bearer, mixed case scheme",
			map[string]string{"Authorization": "BeArEr ss_pat_a"}, "ss_pat_a"},
		{"bearer with padding",
			map[string]string{"Authorization": "Bearer   ss_pat_a  "}, "ss_pat_a"},
		{"custom header", map[string]string{"X-API-Key": "ss_pat_b"}, "ss_pat_b"},
		{"custom header with padding",
			map[string]string{"X-API-Key": "  ss_pat_b  "}, "ss_pat_b"},
		{"bearer wins over the custom header", map[string]string{
			"Authorization": "Bearer ss_pat_a", "X-API-Key": "ss_pat_b"}, "ss_pat_a"},
		{"an empty bearer falls back", map[string]string{
			"Authorization": "Bearer ", "X-API-Key": "ss_pat_b"}, "ss_pat_b"},
		{"another scheme falls back", map[string]string{
			"Authorization": "Basic abc", "X-API-Key": "ss_pat_b"}, "ss_pat_b"},
		{"another scheme with no fallback",
			map[string]string{"Authorization": "Basic abc"}, ""},
		{"nothing at all", map[string]string{}, ""},
	}
	for _, each := range cases {
		t.Run(each.label, func(t *testing.T) {
			headers := http.Header{}
			for name, value := range each.set {
				headers.Set(name, value)
			}
			if got := callercredential.FromHeaders(headers); got != each.expect {
				t.Errorf("read %q, want %q", got, each.expect)
			}
		})
	}
}

// An API key is its own identity. An agent's token is replaced every hour, so it
// is bound with the account's stable identity instead and the choice survives.
func TestTheIdentityIsTheOneGivenThenTheCredentialThenTheLocalCaller(t *testing.T) {
	cases := []struct {
		label      string
		credential string
		identity   string
		want       string
	}{
		{"an API key is its own identity", "ss_pat_a", "", "ss_pat_a"},
		{"a sign-in uses the account", "token.one", "user:u-1", "user:u-1"},
		{"a refreshed sign-in keeps it", "token.two", "user:u-1", "user:u-1"},
	}
	for _, each := range cases {
		ctx := callercredential.Bind(context.Background(), each.credential, each.identity)
		if got := callercredential.Identity(ctx); got != each.want {
			t.Errorf("%s: identity is %q, want %q", each.label, got, each.want)
		}
		if got := callercredential.Credential(ctx); got != each.credential {
			t.Errorf("%s: credential is %q, want %q", each.label, got, each.credential)
		}
	}
}

// Under stdio there is no request and nothing is bound, so every call is the one
// local caller and shares its choice.
func TestWithNothingBoundEveryCallIsTheLocalCaller(t *testing.T) {
	if got := callercredential.Identity(context.Background()); got != callercredential.LocalIdentity {
		t.Errorf("identity is %q, want %q", got, callercredential.LocalIdentity)
	}
	if got := callercredential.Credential(context.Background()); got != "" {
		t.Errorf("credential is %q, want nothing", got)
	}
}
