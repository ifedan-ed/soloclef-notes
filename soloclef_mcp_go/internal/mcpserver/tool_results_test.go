package mcpserver_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/mcpserver"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/settings"
)

// toolResults holds, per call, the text a model reads back against the stand-in
// backend. First captured from the Python server; it changes now only on
// purpose.
const toolResults = "testdata/tool_results.json"

type resultCase struct {
	Label     string         `json:"label"`
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
	Text      string         `json:"text"`
	IsError   bool           `json:"is_error"`
}

func TestToolResultsAreThePublishedTexts(t *testing.T) {
	var cases []resultCase
	raw, err := os.ReadFile(toolResults)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("no captured results to check against")
	}

	backend := fakeBackend(t)
	session, done := connected(t, backend.URL)
	defer done()

	for _, each := range cases {
		t.Run(each.Label, func(t *testing.T) {
			result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
				Name:      each.Tool,
				Arguments: each.Arguments,
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError != each.IsError {
				t.Errorf("isError is %v, want %v", result.IsError, each.IsError)
			}
			if len(result.Content) != 1 {
				t.Fatalf("%d content parts, want one", len(result.Content))
			}
			text, ok := result.Content[0].(*mcp.TextContent)
			if !ok {
				t.Fatalf("content is %T, want text", result.Content[0])
			}
			if text.Text != each.Text {
				t.Errorf("result differs\n got:\n%s\nwant:\n%s", text.Text, each.Text)
			}
		})
	}
}

// The Python server answers a call to a tool it does not have with an errored
// text result, not a protocol error, so the model can read it and correct itself.
func TestAnUnknownToolIsAnErroredResultNotAProtocolError(t *testing.T) {
	backend := fakeBackend(t)
	session, done := connected(t, backend.URL)
	defer done()

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "soloclef_nope", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("an unknown tool was a protocol error: %v", err)
	}
	if !result.IsError {
		t.Error("an unknown tool was not reported as an error")
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content is %T", result.Content[0])
	}
	if text.Text != "Unknown tool: soloclef_nope" {
		t.Errorf("said %q", text.Text)
	}
}

func TestTheServerIntroducesItselfWithTheSameInstructions(t *testing.T) {
	backend := fakeBackend(t)
	session, done := connected(t, backend.URL)
	defer done()

	result := session.InitializeResult()
	if result.Instructions != mcpserver.Instructions {
		t.Errorf("instructions are\n%s", result.Instructions)
	}
	if result.ServerInfo.Name != mcpserver.Name {
		t.Errorf("server name is %q, want %q", result.ServerInfo.Name, mcpserver.Name)
	}
}

func connected(t *testing.T, backendURL string) (*mcp.ClientSession, func()) {
	t.Helper()
	server, _, err := mcpserver.Build(settings.Settings{
		BaseURL:        backendURL,
		APIKey:         "ss_pat_test",
		APIPrefix:      "/api/v1",
		TimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	clientSide, serverSide := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverSide, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	session, err := mcp.NewClient(
		&mcp.Implementation{Name: "test", Version: "0.1.0"}, nil,
	).Connect(ctx, clientSide, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	return session, func() {
		_ = session.Close()
		_ = serverSession.Close()
		cancel()
	}
}
