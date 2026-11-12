// Command chatturn runs one real chat turn, with one tool, against a LiteLLM
// proxy. It is the smallest whole program on the library: a model, a tool,
// an agent, a sink that prints the stream.
//
// It needs three environment variables:
//
//	LITELLM_URL       the proxy's OpenAI-compatible base address,
//	                  for example http://127.0.0.1:4000/v1
//	LITELLM_KEY_FILE  the path of a file holding the proxy key
//	LITELLM_MODEL     the model name as the proxy knows it
//
// With LITELLM_URL or LITELLM_KEY_FILE unset it prints why it is not running
// and exits 0, so it can sit in a script that runs everywhere. With those
// set and anything else wrong (no model named, an unreadable key file, a
// failed call) it exits 1: once it has been told to run, not running is a
// failure, and it does not guess a model.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/openaicompat"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

func main() {
	// Ctrl-C cancels the context, and the context is all the library needs
	// to stop a model call or a tool partway.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	ran, err := run(ctx, os.Getenv, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "chatturn:", err)
		os.Exit(1)
	}
	if !ran {
		fmt.Fprintln(os.Stderr, "chatturn: skipped: set LITELLM_URL and LITELLM_KEY_FILE (and LITELLM_MODEL) to run a real turn")
	}
}

// question is one the model cannot answer without the tool, so a run that
// prints the right hours has shown the whole loop: a model call, the tool
// call it asked for, the result going back, and the answer.
const question = "When does the reading room open on Saturday? Use the opening_hours tool, then answer in one sentence."

// openingHours is the one tool. It only reads, so it runs without asking and
// may run beside other calls.
var openingHours = tool.Tool{
	Name:        "opening_hours",
	Description: "Returns the reading room's opening hours for one day of the week.",
	Parameters:  json.RawMessage(`{"type":"object","properties":{"day":{"type":"string","description":"The day of the week, in English."}},"required":["day"]}`),
	Approval:    tool.Auto,
	Parallel:    true,
	Run: func(_ context.Context, call tool.Call) (string, error) {
		var args struct {
			Day string `json:"day"`
		}
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return "", err
		}
		switch strings.ToLower(strings.TrimSpace(args.Day)) {
		case "saturday":
			return "Saturday: 10:15 to 16:45", nil
		case "sunday":
			return "Sunday: closed", nil
		case "monday", "tuesday", "wednesday", "thursday", "friday":
			return args.Day + ": 08:30 to 20:00", nil
		}
		// An error is shown to the model, which can correct its call.
		return "", fmt.Errorf("%q is not a day of the week", args.Day)
	},
}

// run does the turn, writing what a reader would see to out. It reports
// false, with no error, when the environment does not ask for a run.
func run(ctx context.Context, getenv func(string) string, out io.Writer) (ran bool, err error) {
	base, keyFile := getenv("LITELLM_URL"), getenv("LITELLM_KEY_FILE")
	if base == "" || keyFile == "" {
		return false, nil
	}
	name := getenv("LITELLM_MODEL")
	if name == "" {
		return false, errors.New("LITELLM_MODEL is not set: name the model the proxy should use")
	}
	rawKey, err := os.ReadFile(keyFile)
	if err != nil {
		return false, fmt.Errorf("read the key file: %w", err)
	}

	client, err := openaicompat.New(openaicompat.Config{
		BaseURL: base, APIKey: strings.TrimSpace(string(rawKey)), Model: name,
		// No overall client timeout: it would cut a long answer off in the
		// middle of its stream. The turn's limit is the context below.
		HTTPClient: &http.Client{},
	})
	if err != nil {
		return false, err
	}
	// A busy proxy is tried again; a refused request is not, and neither is
	// a call that failed after it had begun to answer.
	withRetry, err := model.WithRetry(client, model.RetryPolicy{Attempts: 3, Backoff: time.Second, MaxBackoff: 10 * time.Second})
	if err != nil {
		return false, err
	}

	a, err := agent.New(agent.Config{
		Model:            withRetry,
		System:           "You are a concise assistant for a library's front desk.",
		Tools:            []tool.Tool{openingHours},
		MaxSteps:         6,
		MaxParallelTools: 2,
		// One turn, one process: nothing here needs to outlive it. A server
		// passes checkpoint/postgres instead, and a paused turn then
		// resumes in whichever process gets the answer.
		Store: checkpoint.NewMemory(),
	})
	if err != nil {
		return false, err
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	fmt.Fprintf(out, "> %s\n", question)
	res, err := a.Start(ctx, "example", []model.Message{model.User(question)}, printer(out))
	if err != nil {
		return true, err
	}
	fmt.Fprintf(out, "(%d steps, finish reason %q)\n", res.Steps, res.FinishReason)
	return true, nil
}

// printer is the sink: the answer as it streams, and one line per tool call
// and result. A chat server's sink is uistream.Writer.Event instead.
func printer(out io.Writer) event.Sink {
	// midLine is whether the answer's text has left the cursor in the middle
	// of a line, so that a bracketed line starts on a line of its own.
	midLine := false
	line := func(format string, args ...any) {
		if midLine {
			fmt.Fprintln(out)
			midLine = false
		}
		fmt.Fprintf(out, format+"\n", args...)
	}
	return func(ev event.Event) {
		switch ev.Kind {
		case event.TextDelta:
			fmt.Fprint(out, ev.Text)
			midLine = !strings.HasSuffix(ev.Text, "\n")
		case event.ToolCallReady:
			line("[tool %s %s]", ev.Tool, ev.Args)
		case event.ToolResult:
			line("[result %s]", ev.Text)
		case event.ModelFinished:
			line("[model call: %d tokens]", ev.Usage.TotalTokens)
		}
	}
}
