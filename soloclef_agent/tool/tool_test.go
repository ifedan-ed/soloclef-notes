package tool_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

func noop(context.Context, tool.Call) (string, error) { return "", nil }

func TestValidateNamesTheFirstThingWrong(t *testing.T) {
	good := tool.Tool{Name: "search_notes-2", Parameters: []byte(`{"type":"object"}`), Approval: tool.Auto, Run: noop}
	if err := good.Validate(); err != nil {
		t.Fatalf("a good tool was refused: %v", err)
	}
	cases := map[string]func(*tool.Tool){
		"must be 1 to 64 characters":              func(tl *tool.Tool) { tl.Name = "" },
		`tool name "a.b"`:                         func(tl *tool.Tool) { tl.Name = "a.b" },
		`tool name "` + strings.Repeat("x", 65):   func(tl *tool.Tool) { tl.Name = strings.Repeat("x", 65) },
		"has no Run function":                     func(tl *tool.Tool) { tl.Run = nil },
		"does not say whether it needs approval":  func(tl *tool.Tool) { tl.Approval = 0 },
		"set Approval to tool.Auto":               func(tl *tool.Tool) { tl.Approval = 9 },
		"Parameters must be a JSON Schema object": func(tl *tool.Tool) { tl.Parameters = []byte(`"string"`) },
	}
	for want, breakIt := range cases {
		tl := good
		breakIt(&tl)
		if err := tl.Validate(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Validate error = %v, want one containing %q", err, want)
		}
	}
}

func TestAskUserCarriesItsPayloadThroughWrapping(t *testing.T) {
	err := tool.AskUser(map[string]string{"type": "structured_question"})
	// A tool may wrap the error on its way out; the loop must still see it.
	wrapped := errors.Join(errors.New("context"), err)
	q, ok := tool.AsQuestion(wrapped)
	if !ok || string(q.Payload) != `{"type":"structured_question"}` {
		t.Fatalf("question = %+v, %v", q, ok)
	}
	if _, ok := tool.AsQuestion(errors.New("an ordinary failure")); ok {
		t.Fatal("an ordinary error was taken for a question")
	}
	// A payload that cannot be encoded is an ordinary error, not a question
	// with nothing in it.
	if _, ok := tool.AsQuestion(tool.AskUser(make(chan int))); ok {
		t.Fatal("an unencodable payload became a question")
	}
}

func TestWithTimeoutTurnsItsOwnDeadlineIntoAToolErrorOnly(t *testing.T) {
	waits := tool.Tool{Name: "waits", Parameters: []byte(`{}`), Approval: tool.Auto,
		Run: func(ctx context.Context, _ tool.Call) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		}}
	limited, err := tool.WithTimeout(waits, 5*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}

	_, err = limited.Run(context.Background(), tool.Call{})
	if err == nil || err.Error() != "waits did not finish within 5ms" || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want a plain timeout message the model can read", err)
	}

	// When the caller's own context ends, that is what is reported: the run
	// is stopping, and the loop must not mistake it for a slow tool.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := limited.Run(ctx, tool.Call{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}

	// The original tool is untouched: wrapping copies.
	if waits.Run == nil || limited.Name != "waits" {
		t.Fatal("wrapping changed the tool it was given")
	}
	for _, bad := range []func() (tool.Tool, error){
		func() (tool.Tool, error) { return tool.WithTimeout(waits, 0) },
		func() (tool.Tool, error) { return tool.WithTimeout(tool.Tool{}, time.Second) },
		func() (tool.Tool, error) { return tool.WithTimeout(tool.Tool{Name: "x"}, time.Second) },
	} {
		if _, err := bad(); err == nil {
			t.Error("an unusable timeout wrapper was built")
		}
	}
}
