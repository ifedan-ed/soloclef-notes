package restclient_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/callercredential"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/restclient"
)

// The sentences below are what the Python client raised for each status and body
// shape, taken from it over HTTP. They are what the model reads when a call is
// refused, so each one is checked rather than only the status.
func TestFailureSentencesMatchPythonClient(t *testing.T) {
	cases := []struct {
		label  string
		status int
		body   string
		want   string
	}{
		{"401 with detail", 401, `{"detail": "Invalid token"}`,
			"Authentication failed — the Soloclef Notes API key is invalid or " +
				"expired. (server said: Invalid token)"},
		{"401 with no body", 401, "",
			"Authentication failed — the Soloclef Notes API key is invalid or expired."},
		{"402", 402, `{"detail": "Out of credits"}`,
			"The account is out of credits for this operation. (server said: Out of credits)"},
		{"403", 403, `{"detail": "API access is disabled"}`,
			"Access denied — the token lacks permission, or API access is switched " +
				"off for this account (switch it on in Soloclef Notes, Settings, API access). " +
				"(server said: API access is disabled)"},
		{"404 with no body", 404, "", "The requested resource was not found."},
		{"429", 429, `{"detail": "slow down"}`,
			"Rate limited by the backend — retry after a short pause. (server said: slow down)"},
		{"unhinted status with detail", 400, `{"detail": "Synthetic refusal."}`,
			"Soloclef Notes returned 400: Synthetic refusal."},
		{"unhinted status with no body", 500, "", "Soloclef Notes returned HTTP 500."},
		{"detail is an object with a message", 400,
			`{"detail": {"message": "Inner message.", "code": "x"}}`,
			"Soloclef Notes returned 400: Inner message."},
		{"detail is an object with no message", 400, `{"detail": {"code": "x"}}`,
			"Soloclef Notes returned 400: {'code': 'x'}"},
		{"detail is a list", 400, `{"detail": ["a", 1]}`,
			"Soloclef Notes returned 400: ['a', 1]"},
		{"detail is null", 400, `{"detail": null}`,
			"Soloclef Notes returned 400: None"},
		{"no detail member", 400, `{"other": "value"}`,
			"Soloclef Notes returned 400: {'other': 'value'}"},
		{"body is a bare list", 400, `["a", "b"]`,
			"Soloclef Notes returned 400: ['a', 'b']"},
		{"body is a bare string", 400, `"plain words"`,
			"Soloclef Notes returned 400: plain words"},
		{"body is a bare number", 400, `7`, "Soloclef Notes returned 400: 7"},
		{"body is not JSON", 400, "<<<not json>>>",
			"Soloclef Notes returned 400: <<<not json>>>"},
	}
	for _, each := range cases {
		t.Run(each.label, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(
				func(writer http.ResponseWriter, _ *http.Request) {
					writer.WriteHeader(each.status)
					_, _ = writer.Write([]byte(each.body))
				}))
			defer backend.Close()
			client := restclient.New(backend.URL, time.Second, "ss_pat_test")
			_, err := client.Do(context.Background(),
				restclient.Call{Method: "GET", Path: "/anything"})
			if err == nil {
				t.Fatal("expected a refusal")
			}
			if err.Error() != each.want {
				t.Errorf("got:\n %s\nwant:\n %s", err.Error(), each.want)
			}
		})
	}
}

func TestAnUnreachableBackendNamesItsAddress(t *testing.T) {
	// A closed port rather than a stopped server, so the failure is a refused
	// connection and not a timeout the test has to wait for.
	backend := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := backend.URL
	backend.Close()
	client := restclient.New(address, time.Second, "ss_pat_test")
	_, err := client.Do(context.Background(), restclient.Call{Method: "GET", Path: "/x"})
	if err == nil {
		t.Fatal("expected a refusal")
	}
	const opening = "Could not reach Soloclef Notes at " + ""
	if got := err.Error(); len(got) < len(opening) || got[:len(opening)] != opening {
		t.Errorf("refusal does not open with the unreachable sentence: %s", got)
	}
	if !hasSuffix(err.Error(),
		"Confirm the backend is running and SURFSENSE_BASE_URL is correct.") {
		t.Errorf("refusal does not close with the advice: %s", err.Error())
	}
}

func TestACallWithNoCredentialIsRefusedBeforeTheRequest(t *testing.T) {
	var reached bool
	backend := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, _ *http.Request) {
			reached = true
			_, _ = writer.Write([]byte(`{}`))
		}))
	defer backend.Close()
	client := restclient.New(backend.URL, time.Second, "")
	_, err := client.Do(context.Background(), restclient.Call{Method: "GET", Path: "/x"})
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if reached {
		t.Error("the backend was called without a credential")
	}
	const want = "No Soloclef Notes API key supplied. Send it as an 'Authorization: " +
		"Bearer ss_pat_...' header (remote server), or set the SURFSENSE_API_KEY " +
		"environment variable (stdio)."
	if err.Error() != want {
		t.Errorf("got:\n %s\nwant:\n %s", err.Error(), want)
	}
}

// The caller's own credential wins over the environment's, which is what makes
// one process able to serve many users.
func TestTheRequestCredentialWinsOverTheEnvironmentKey(t *testing.T) {
	var seen string
	backend := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			seen = request.Header.Get("Authorization")
			_, _ = writer.Write([]byte(`{}`))
		}))
	defer backend.Close()
	client := restclient.New(backend.URL, time.Second, "ss_pat_environment")

	if _, err := client.Do(context.Background(),
		restclient.Call{Method: "GET", Path: "/x"}); err != nil {
		t.Fatal(err)
	}
	if seen != "Bearer ss_pat_environment" {
		t.Errorf("without a request credential the header was %q", seen)
	}

	ctx := callercredential.Bind(context.Background(), "ss_pat_caller", "")
	if _, err := client.Do(ctx, restclient.Call{Method: "GET", Path: "/x"}); err != nil {
		t.Fatal(err)
	}
	if seen != "Bearer ss_pat_caller" {
		t.Errorf("with a request credential the header was %q", seen)
	}
}

func hasSuffix(text, suffix string) bool {
	return len(text) >= len(suffix) && text[len(text)-len(suffix):] == suffix
}
