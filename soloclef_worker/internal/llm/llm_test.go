package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/proxystub"
)

func proxyFor(stub *proxystub.Server) Proxy {
	return Proxy{
		BaseURL: stub.BaseURL(), Key: "test-key", HTTP: &http.Client{}, CallTimeout: 10 * time.Second,
		Retry: model.RetryPolicy{Attempts: 2, Backoff: time.Millisecond, MaxBackoff: time.Millisecond},
	}
}

type title struct {
	Title string `json:"title"`
}

func readTitle(raw []byte) (title, error) {
	var value title
	if err := json.Unmarshal(raw, &value); err != nil {
		return title{}, err
	}
	if value.Title == "" {
		return title{}, Reject("- title: must not be empty")
	}
	return value, nil
}

func askTitle(t *testing.T, stub *proxystub.Server) (title, error) {
	t.Helper()
	proxy := proxyFor(stub)
	client, err := proxy.Client("deepseek-v4.1-flash", true)
	if err != nil {
		t.Fatal(err)
	}
	return AskJSON(context.Background(), client, proxy.CallTimeout,
		[]model.Message{{Role: model.RoleSystem, Content: "Name it."}, {Role: model.RoleUser, Content: "material"}}, readTitle)
}

// Prose around the object, a code fence and a raw newline inside a string
// are each a reply that is right in substance: read, not re-asked.
func TestAChattyReplyIsReadWithoutARetry(t *testing.T) {
	stub := proxystub.Start()
	defer stub.Close()
	stub.Chat = func(proxystub.ChatRequest) (string, int) {
		return "Sure! Here it is:\n```json\n{\"title\": \"Heart\nFailure\"}\n```", http.StatusOK
	}
	got, err := askTitle(t, stub)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Heart\nFailure" {
		t.Fatalf("title %q", got.Title)
	}
	if calls := len(stub.Chats()); calls != 1 {
		t.Fatalf("%d calls, want 1", calls)
	}
}

func TestARefusedReplyIsAskedForOnceMoreWithTheProblems(t *testing.T) {
	stub := proxystub.Start()
	defer stub.Close()
	replies := []string{`{"title": ""}`, `{"title": "Sepsis in Children"}`}
	stub.Chat = func(proxystub.ChatRequest) (string, int) {
		reply := replies[0]
		replies = replies[1:]
		return reply, http.StatusOK
	}
	got, err := askTitle(t, stub)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Sepsis in Children" {
		t.Fatalf("title %q", got.Title)
	}
	chats := stub.Chats()
	if len(chats) != 2 {
		t.Fatalf("%d calls, want 2", len(chats))
	}
	if !strings.Contains(chats[1].Last(), "title: must not be empty") {
		t.Fatalf("the retry did not show the model its mistake: %q", chats[1].Last())
	}
	if chats[0].Thinking["type"] != "disabled" {
		t.Fatalf("a DeepSeek model was not asked to answer without reasoning: %v", chats[0].Thinking)
	}
}

func TestTwoRefusedRepliesAreAFailureNotALoop(t *testing.T) {
	stub := proxystub.Start()
	defer stub.Close()
	stub.Chat = func(proxystub.ChatRequest) (string, int) { return "no idea", http.StatusOK }
	if _, err := askTitle(t, stub); !errors.Is(err, ErrUnusable) {
		t.Fatalf("got %v, want ErrUnusable", err)
	}
	if calls := len(stub.Chats()); calls != 2 {
		t.Fatalf("%d calls, want exactly 2", calls)
	}
}

// A provider failure is the platform failing: it is returned, and no
// correction is sent.
func TestAProviderRefusalIsNotCorrected(t *testing.T) {
	stub := proxystub.Start()
	defer stub.Close()
	stub.Chat = func(proxystub.ChatRequest) (string, int) { return "", http.StatusBadRequest }
	_, err := askTitle(t, stub)
	var refused *model.Error
	if !errors.As(err, &refused) || refused.Status != http.StatusBadRequest {
		t.Fatalf("got %v, want the proxy's 400", err)
	}
}

func TestOnlyAKnownFamilyIsSentTheThinkingSwitch(t *testing.T) {
	stub := proxystub.Start()
	defer stub.Close()
	stub.Chat = func(proxystub.ChatRequest) (string, int) { return `{"title":"x"}`, http.StatusOK }
	proxy := proxyFor(stub)
	client, err := proxy.Client("gpt-5-mini", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AskJSON(context.Background(), client, time.Second, []model.Message{{Role: model.RoleUser, Content: "x"}}, readTitle); err != nil {
		t.Fatal(err)
	}
	if thinking := stub.Chats()[0].Thinking; thinking != nil {
		t.Fatalf("a model that does not take the switch was sent it: %v", thinking)
	}
}
