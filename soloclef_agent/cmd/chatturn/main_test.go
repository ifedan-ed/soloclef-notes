package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func env(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

func TestItSkipsCleanlyWhenNotAskedToRun(t *testing.T) {
	for _, vars := range []map[string]string{
		{},
		{"LITELLM_URL": "http://127.0.0.1:1/v1"},
		{"LITELLM_KEY_FILE": "/nonexistent"},
	} {
		var out bytes.Buffer
		ran, err := run(context.Background(), env(vars), &out)
		if ran || err != nil || out.Len() != 0 {
			t.Fatalf("with %v: ran=%v err=%v output=%q", vars, ran, err, out.String())
		}
	}
}

func TestOnceAskedToRunAnythingMissingIsAnError(t *testing.T) {
	key := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(key, []byte("k\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err := run(context.Background(), env(map[string]string{"LITELLM_URL": "http://127.0.0.1:1/v1", "LITELLM_KEY_FILE": key}), &out); err == nil || !strings.Contains(err.Error(), "LITELLM_MODEL") {
		t.Fatalf("no model named: %v", err)
	}
	if _, err := run(context.Background(), env(map[string]string{"LITELLM_URL": "http://127.0.0.1:1/v1", "LITELLM_KEY_FILE": key + ".missing", "LITELLM_MODEL": "m"}), &out); err == nil {
		t.Fatal("an unreadable key file was accepted")
	}
}

// The whole program against a stand-in for the proxy: the first call asks
// for the tool, the second answers from its result.
func TestATurnWithTheToolAgainstAnEndpoint(t *testing.T) {
	var calls atomic.Int32
	var auth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "text/event-stream")
		lines := []string{
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"opening_hours","arguments":"{\"day\":\"Saturday\"}"}}]}}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		}
		if calls.Add(1) == 2 {
			lines = []string{
				`data: {"choices":[{"delta":{"content":"It opens at 10:15."}}]}`,
				`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			}
		}
		for _, line := range append(lines, `data: [DONE]`) {
			fmt.Fprintf(w, "%s\n\n", line)
		}
	}))
	defer srv.Close()
	key := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(key, []byte("secret-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	ran, err := run(context.Background(), env(map[string]string{"LITELLM_URL": srv.URL + "/v1", "LITELLM_KEY_FILE": key, "LITELLM_MODEL": "m"}), &out)
	if !ran || err != nil {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	for _, want := range []string{`[tool opening_hours {"day":"Saturday"}]`, "[result Saturday: 10:15 to 16:45]", "It opens at 10:15.", "(3 steps"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output lacks %q:\n%s", want, out.String())
		}
	}
	if auth.Load() != "Bearer secret-key" || strings.Contains(out.String(), "secret-key") {
		t.Fatalf("key handling: sent %q; output %q", auth.Load(), out.String())
	}
}
