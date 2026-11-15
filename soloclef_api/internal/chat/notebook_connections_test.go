package chat

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/cite"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapaccounts"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapmail"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/mailtools"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/mcptools"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/mcpserver"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/sealedsecret"
)

// The check the notebook chat's connections rest on: a turn in a
// notebook's chat reads the person's Stalwart mail and calls their MCP
// server, and the answer cites both the way it cites a web page.
//
// Everything between the model and the two servers is the production code:
// the assembly, the agent loop, the mail tools, the MCP client (mcptools,
// through the adapter a deployment uses) talking the real protocol over
// HTTP to a real MCP server (the SDK's server side) on loopback. The model
// is scripted, the JMAP server is a stand-in at the mail tools' JMAP port
// (its wire is jmapmail's own tests' to check), and the rows are in memory.
func TestANotebookChatTurnReadsMailAndCallsAConnectedApp(t *testing.T) {
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "tickets-under-test", Version: "1.0"}, nil)
	var asked []string
	var mu sync.Mutex
	server.AddTool(&mcp.Tool{
		Name: "lookup_ticket", Description: "Look a ticket up by its number.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"number":{"type":"integer"}},"required":["number"]}`),
	}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		mu.Lock()
		asked = append(asked, string(request.Params.Arguments))
		mu.Unlock()
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Ticket 7 is open: the invoice total is disputed."}}}, nil
	})
	remote := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer remote.Close()

	seal, err := sealedsecret.New("fake-deployment-secret", sealedsecret.MCPServerSecret)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := mcpserver.Build(seal, ada, mcpserver.Settings{Address: remote.URL, Transport: mcpserver.StreamableHTTP,
		Auth: mcpserver.Auth{Kind: mcpserver.AuthNone}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(map[string]any{"server_config": stored})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	source, err := mcptools.NewSource(mcptools.Settings{
		Rows: &appRows{owner: ada, row: mcptools.Connection{ID: 41, Name: "Tickets", Config: config}}, Generations: &fixedGeneration{},
		Network: loopbackOnly{}, Seal: seal,
		OAuth:  mcptools.OAuth{Network: loopbackOnly{}, Timeout: 5 * time.Second, ClientName: "chat-under-test"},
		Logger: logger, Now: time.Now, ClientName: "chat-under-test", SiteHostsWhenUnset: []string{},
		ReservedNames: mailtools.Names(),
		Limits: mcptools.Limits{ToolsPerServer: 200, Discovery: 5 * time.Second, Call: 5 * time.Second,
			Lifetime: time.Minute, RetryIncomplete: time.Second, Accounts: 10, ResultBytes: 4000},
	})
	if err != nil {
		t.Fatal(err)
	}

	script := modeltest.NewScript(
		modeltest.Reply{Calls: []model.ToolCall{{ID: "m1", Name: mailtools.SearchTool, Args: `{"text":"invoice"}`}}},
		modeltest.Reply{Calls: []model.ToolCall{{ID: "a1", Name: "lookup_ticket", Args: `{"number":7}`}}},
		modeltest.Reply{Text: "Ann asked about the invoice [1], and its ticket is still open [2]. The notes agree [3]."},
	)
	data := newRows()
	data.about.Notebook = &Notebook{ID: 5, Title: "Accounts", DocumentIDs: []int64{12}, SourceTitles: []string{"Ledger"}}
	built := withAccounts(t, script, data, assembly.ConnectedAppsOf(source))
	mail := built.Mail.(*mailtools.Tools)
	mail.Rows, mail.JMAP = noStoredMailboxes{}, stalwart{}

	turn, err := built.Build(ctx, Turn{ThreadID: 1, UserID: ada, Scope: "documents"})
	if err != nil {
		t.Fatal(err)
	}
	defer turn.Close()

	// The turn runs to its answer, every call approved as a person would:
	// whether a call waits is the permission settings' business, and either
	// way it must reach the two servers.
	result, err := turn.Agent.Start(ctx, "notebook-connections", []model.Message{model.User("What did Ann say about the invoice, and is its ticket open?")}, event.Discard)
	for err == nil && result.Outcome == agent.Paused {
		waiting, pending := turn.Agent.Pending(ctx, "notebook-connections")
		if pending != nil {
			t.Fatal(pending)
		}
		decisions := make([]agent.Decision, 0, len(waiting))
		for _, call := range waiting {
			decisions = append(decisions, agent.Decision{CallID: call.CallID, Verdict: agent.Approve})
		}
		result, err = turn.Agent.Resume(ctx, "notebook-connections", decisions, event.Discard)
	}
	if err != nil || result.Outcome != agent.Finished {
		t.Fatalf("the turn ended %v: %v", result.Outcome, err)
	}

	// The model was given both, beside the notebook's own search, and told
	// the notebook comes first.
	first := script.Requests()[0]
	var offered []string
	for _, spec := range first.Tools {
		offered = append(offered, spec.Name)
	}
	if !strings.Contains(" "+strings.Join(offered, " ")+" ", " search_knowledge_base ") || !strings.Contains(strings.Join(offered, " "), mailtools.SearchTool) ||
		!strings.Contains(strings.Join(offered, " "), "lookup_ticket") {
		t.Fatalf("the notebook chat was offered %v", offered)
	}
	if system := first.Messages[0].Content; !strings.Contains(system, "they are your primary material") || !strings.Contains(system, "<connected_apps>") {
		t.Errorf("the prompt does not put the sources first or name the apps:\n%s", system)
	}

	// The MCP server was really called, with the model's arguments.
	mu.Lock()
	if len(asked) != 1 || !strings.Contains(asked[0], `"number":7`) {
		t.Errorf("the MCP server was asked %v", asked)
	}
	mu.Unlock()

	// Both results came back numbered, and the answer's labels resolve to
	// a mail chip and an app chip; the invented [3] resolves to nothing.
	results := script.Requests()[2].Messages
	var mailResult, appResult string
	for _, message := range results {
		switch message.ToolCallID {
		case "m1":
			mailResult = message.Content
		case "a1":
			appResult = message.Content
		}
	}
	if !strings.Contains(mailResult, "- [1] received 2026-11-12") || !strings.HasPrefix(appResult, "[2] Result from the connected app Tickets") ||
		!strings.Contains(appResult, "Ticket 7 is open") {
		t.Fatalf("the results were not numbered:\nmail: %s\napp: %s", mailResult, appResult)
	}
	saved := cite.Rewrite(result.Answer, cite.FromSources(result.Citations))
	if strings.Contains(saved, "[3]") || strings.Count(saved, "[citation:src_") != 2 {
		t.Fatalf("the saved answer is %q", saved)
	}
	kind, label, ok := cite.LabelOf(result.Citations[0].Ref)
	if !ok || kind != cite.Mail || label != "Ann Smith <ann@example.org> · Invoice 4411 · 2026-11-12" {
		t.Errorf("the mail chip is %q %q %v", kind, label, ok)
	}
	if kind, label, ok = cite.LabelOf(result.Citations[1].Ref); !ok || kind != cite.App || label != "Tickets · lookup_ticket" {
		t.Errorf("the app chip is %q %q %v", kind, label, ok)
	}
	// And the step display draws both as the person's connections.
	if turn.Activities[mailtools.SearchTool].Category != "connector" || turn.Activities["lookup_ticket"].IntegrationName != "Tickets" {
		t.Errorf("the step display has %+v and %+v", turn.Activities[mailtools.SearchTool], turn.Activities["lookup_ticket"])
	}
}

// appRows is one account's one MCP connection, in memory.
type appRows struct {
	owner string
	row   mcptools.Connection
}

func (r *appRows) Connections(_ context.Context, userID string) ([]mcptools.Connection, error) {
	if userID != r.owner {
		return nil, nil
	}
	return []mcptools.Connection{r.row}, nil
}

func (r *appRows) Connection(_ context.Context, userID string, connectorID int64) (*mcptools.Connection, error) {
	if userID != r.owner || connectorID != r.row.ID {
		return nil, nil
	}
	row := r.row
	return &row, nil
}

func (r *appRows) AddTrusted(context.Context, string, int64, string) (bool, error) { return true, nil }
func (r *appRows) StoreDefinitions(context.Context, string, int64, []byte) error   { return nil }
func (r *appRows) MarkAuthExpired(context.Context, string, int64) (bool, error)    { return false, nil }
func (r *appRows) StoreTokens(context.Context, string, int64, string, string, int64) (bool, error) {
	return false, nil
}

// fixedGeneration is a counter nothing bumps.
type fixedGeneration struct{}

func (*fixedGeneration) Current(context.Context, string) (int64, error) { return 1, nil }
func (*fixedGeneration) Bump(context.Context, string) error             { return nil }

// loopbackOnly lets a connection reach the test's own server, which the
// production guard (mcptools.PublicInternet) exists to refuse.
type loopbackOnly struct{}

func (loopbackOnly) Client(string, []string) (*http.Client, error) {
	return &http.Client{Timeout: 10 * time.Second}, nil
}

// noStoredMailboxes is an account whose mail is a JMAP mailbox alone.
type noStoredMailboxes struct{}

func (noStoredMailboxes) Mailboxes(context.Context, string) ([][]byte, error) { return nil, nil }
func (noStoredMailboxes) Parser(context.Context, string) (string, error)      { return "", nil }

// stalwart is Ada's Stalwart mailbox as the mail tools' JMAP port sees it,
// holding one message.
type stalwart struct{ noJMAP }

func (stalwart) Accounts(_ context.Context, userID string) ([]jmapaccounts.Account, error) {
	if userID != ada {
		return nil, nil
	}
	return []jmapaccounts.Account{{ID: "stalwart", Label: "Work", ServerURL: "https://mail.example.org", Username: "ada@example.org", CanSend: true}}, nil
}

func (stalwart) Search(context.Context, string, string, string, jmapmail.Query) ([]jmapmail.Summary, bool, error) {
	return []jmapmail.Summary{{ID: "M1", From: "Ann Smith <ann@example.org>", Subject: "Invoice 4411",
		ReceivedAt: time.Date(2026, 11, 12, 9, 30, 0, 0, time.UTC)}}, false, nil
}
