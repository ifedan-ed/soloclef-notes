// Package assembly builds the agent one chat turn runs: which tools this
// account has for this message, the prompt that describes them, and the
// model that answers. The API tier answers a chat turn with it and the
// worker runs an automation as one headless turn of it, so a scope offers
// the same tools and the prompt says the same things wherever a turn runs.
//
// Two parts of a turn are handed in rather than held here, because what
// they need belongs to one tier: the tool that writes the asker's memory
// (Remembering) and the specialist that makes artifacts (ArtifactMaker).
// Each is named in Assembly, and an Assembly without one offers no such
// tool and writes no such block of the prompt.
package assembly

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/academic"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/documents"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/pagereader"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/websearch"
)

// pick is one entry of the composer's Tools menu: a source the person can
// choose for a message.
type pick struct {
	// label is the name the person saw; tool is what the model calls.
	label string
	tool  string
	// provider is set for a source that needs the account's own key.
	provider websearch.Provider
}

// picks are the sources a message may name in "preferred_tool". Each is one
// tool: Exa and Tavily are separate accounts, and PubMed, arXiv and the
// preprint servers are separate literatures. None stands in for another.
//
// The platform scrapers the menu once listed (Reddit, TikTok and the rest)
// are cut from the product, and a request naming one is refused by name
// rather than answered with some other tool.
var picks = map[string]pick{
	"exa":       {label: "Exa", tool: websearch.Exa.ToolName(), provider: websearch.Exa},
	"tavily":    {label: "Tavily", tool: websearch.Tavily.ToolName(), provider: websearch.Tavily},
	"pubmed":    {label: "PubMed", tool: academic.PubMedTool},
	"arxiv":     {label: "arXiv", tool: academic.ArxivTool},
	"preprints": {label: "bioRxiv and medRxiv", tool: academic.PreprintTool},
}

// Turn is what a turn's agent is built from: who is asking, where, and what
// they chose for this message. It is saved while a turn waits for approval
// so the resumed turn is built from the same choices.
type Turn struct {
	ThreadID int64 `json:"thread_id"`
	// UserID is the account the turn runs for: the thread's owner, taken
	// from the request's principal. A resumed turn sets it again from the
	// thread the gate answered, so the saved value never decides access.
	UserID string `json:"user_id"`
	// Scope is the retrieval scope: documents, web or all.
	Scope string `json:"scope"`
	// PreferredTool is the Tools menu pick, or "".
	PreferredTool string `json:"preferred_tool,omitempty"`
	// DocumentIDs and FolderIDs are the sources mentioned with @.
	DocumentIDs []int64 `json:"document_ids,omitempty"`
	FolderIDs   []int64 `json:"folder_ids,omitempty"`
	// DisabledTools are the tools the person switched off for the chat.
	DisabledTools []string `json:"disabled_tools,omitempty"`
	// Unattended is set for a turn nobody is present for (an automation's
	// run) and nil for a chat's. It is never read from a request or from a
	// saved turn, which is why it has no JSON name: only the code that
	// starts such a turn can set it.
	Unattended *Unattended `json:"-"`
}

// Unattended is the trust an unattended turn runs under. It replaces every
// standing "always allow" the account has given in chat: what a person
// allowed while watching is not what they allowed to happen with nobody
// there, so the one rule consulted is the automation's own.
type Unattended struct {
	// TrustAskFirst lets a tool that asks first run without asking. A tool
	// that must always ask is not affected by it: the agent loop never
	// consults trust for one, so it waits, and the runner refuses it.
	TrustAskFirst bool
	// Model is the proxy's name for the model that answers the turn, when
	// the automation or its step names one; "" is the account's own chat
	// model. A name the proxy does not list fails the turn and is never
	// replaced by another.
	Model string
	// NotebookID confines the turn to one of the account's notebooks, as
	// that notebook's own chat is confined; 0 is no confinement. The rows
	// the assembly is given read the notebook under the account
	// (PromptRows.Context), so an id that is not the account's confines
	// the turn to nothing rather than to another account's sources.
	NotebookID int64
}

// switchable are the tools the composer's tool list offers to switch off,
// in the order it shows them: GET /agent/tools answers exactly this, and
// Build leaves out exactly the ones a turn names. It is every tool of the
// chat's own except the artifact specialist, which the prompt's own
// <artifacts> block describes and is not a thing to switch.
var switchable = []Switchable{
	{Name: documents.ToolName, Description: "Search your documents and notes"},
	{Name: websearch.Exa.ToolName(), Description: "Search the web with Exa, when you have connected it"},
	{Name: websearch.Tavily.ToolName(), Description: "Search the web with Tavily, when you have connected it"},
	{Name: academic.PubMedTool, Description: "Search PubMed, with full text from PubMed Central"},
	{Name: academic.ArxivTool, Description: "Search arXiv"},
	{Name: academic.PreprintTool, Description: "Search bioRxiv and medRxiv preprints"},
	{Name: pagereader.ToolName, Description: "Read a web page in full, asking first"},
	{Name: MemoryToolName, Description: "Remember durable facts and preferences between conversations"},
}

// Switchable is one tool the composer's tool list offers to switch off.
type Switchable struct {
	Name        string
	Description string
}

// SwitchableTools is the list GET /agent/tools answers.
func SwitchableTools() []Switchable { return append([]Switchable(nil), switchable...) }

// IsPick reports whether name is one of the Tools menu's sources.
func IsPick(name string) bool {
	_, picked := picks[name]
	return picked
}

// The retrieval scopes a turn may name.
const (
	ScopeDocuments = "documents"
	ScopeWeb       = "web"
	ScopeAll       = "all"
)

// MemoryToolName is what the model calls to write the asker's memory, and
// what the page's memory card is keyed to.
const MemoryToolName = "update_memory"

// MemoryTarget is the memory document a turn's update_memory tool replaces.
type MemoryTarget struct {
	// UserID is the account whose memory this is: the asker.
	UserID string
	// Shown is the document as it was read for this turn's prompt: what the
	// model believes it is replacing.
	Shown string
}

// Built is a turn's agent and what the route needs to know about it.
type Built struct {
	Agent *agent.Agent
	// Chat is the model that answers the turn: the one the agent calls, and
	// the one that names a new thread and summarises a long one. One turn
	// uses one model for all three, the account's.
	Chat model.Model
	// Model is the name usage is recorded under.
	Model string
	// Preface is put before the person's message for the model: what they
	// picked in the Tools menu. It is not saved in the message row.
	Preface string
	// Activities are the turn's own tools as the step display words them:
	// the ones that exist only for this turn (an account's connected
	// apps). Tools the package always has are described in activity.go.
	Activities map[string]Activity
	// Trust remembers "always allow" for a tool of this turn, wherever that
	// tool's trust is kept.
	Trust func(ctx context.Context, tool string) error
	// Close releases what the turn opened (connections to an account's
	// connected apps). It is called once, when the turn is over or when the
	// request that built the agent ends without running it.
	Close func()
}

// Assembler builds the agent for a turn. It is an interface so that the
// routes can be tested with a scripted model and hand-made tools.
type Assembler interface {
	Build(ctx context.Context, turn Turn) (Built, error)
}

// Limits bound one turn's run. All are required.
type Limits struct {
	// MaxSteps is how many steps (a model call or a batch of tool calls)
	// one turn may take. Per turn: it starts again with each message.
	MaxSteps int
	// MaxParallelTools is how many tool calls of one batch run at once.
	MaxParallelTools int
	// Continuations is how many times an answer cut off by the model's
	// output cap is carried on within one model step.
	Continuations int
}

// PromptRows is what the assembly reads per turn.
type PromptRows interface {
	// Context is what the turn's prompt and confinement come from; see
	// TurnContext.
	Context(ctx context.Context, turn Turn) (TurnContext, error)
	// Trusted reports whether the account has said "always allow" for tool.
	Trusted(ctx context.Context, userID, tool string) (bool, error)
	// Trust remembers "always allow" for a tool.
	Trust(ctx context.Context, userID, tool string) error
}

// Connections are an account's connected apps (MCP servers), as tools.
type Connections interface {
	// ForTurn lists the account's tools for one turn. Nothing is
	// connected to until a tool is called. The account is the turn's
	// owner: whose connections they are and who is asking are one id.
	ForTurn(ctx context.Context, userID string) (ConnectedTools, error)
}

// ConnectedTools are one turn's connected-app tools.
type ConnectedTools interface {
	Tools() []tool.Tool
	// Trusted reports whether the person has said "always allow" for the
	// tool, read from its connection's row now. mine is false for a name
	// that is not one of these tools.
	Trusted(ctx context.Context, name string) (trusted, mine bool, err error)
	// Trust remembers "always allow" for one of these tools.
	Trust(ctx context.Context, name string) error
	// App names the connected app a tool belongs to, for the step display.
	App(name string) (app string, mine bool)
	Close()
}

// AccountTools are tools made from something the account itself connected
// under Connectors: its mailboxes (mailtools), its Google Drive
// (drivetools), its Nextcloud (nextcloudtools). Unlike a connected app they are this product's own tools,
// so the prompt describes them; like one, they exist only for an account
// that connected the thing, and each call asks first.
type AccountTools interface {
	// Connected reports whether the account has it connected and switched
	// on. An account that has not is offered none of the tools.
	Connected(ctx context.Context, userID string) (bool, error)
	// ForTurn is the tools, for the turn's owner: whose mailbox or Drive
	// they reach is fixed here and is not an argument of any of them.
	ForTurn(userID string) []tool.Tool
}

// Remembering makes a turn's update_memory tool: the one that replaces the
// memory document named by target (the API tier's own writer), and words
// the two lines of the prompt that are about writing it.
type Remembering interface {
	Tool(target MemoryTarget) tool.Tool
	// Protocol is the prompt's instructions for when and how to write.
	Protocol() string
	// Usage says how full a document is, for the memory block's heading.
	Usage(document string) string
}

// ArtifactMaker is the specialist that makes artifacts (the API tier's
// artifact specialist), as a turn is given it.
type ArtifactMaker interface {
	// Delegate is the specialist for one turn: run on the turn's model, and
	// saving what it makes for the turn's account under the turn's thread.
	Delegate(chat model.Model, userID string, threadID int64) (agent.Delegate, error)
	// Prompt is the body of the main agent's <artifacts> block.
	Prompt() string
	// Check reports what the specialist is missing, with a model in place.
	Check(chat model.Model) error
}

// Assembly is the production Assembler: the model, the tools and the
// specialist a deployment has, put together per turn.
//
// Building per turn is deliberate. agent.New validates and fills two small
// maps (tens of microseconds), so there is no compiled agent to cache, and
// so nothing about a person (their account's model and keys, what they
// trust, what they picked, the notebook they are in) can outlive the turn it
// was read for.
type Assembly struct {
	// Models decides which model answers the turn's account.
	Models *Models
	Store  checkpoint.Store
	Rows   PromptRows

	Documents *documents.Searcher
	Web       websearch.Search
	Academic  *academic.Sources
	// Pages is the page reader: it fetches addresses the model chose, so
	// it asks every time (tool.AlwaysAsk).
	Pages tool.Tool
	// Connected are the account's connected apps.
	Connected Connections
	// Mail reads the account's connected mailboxes and Drive works with
	// its Google Drive. Each is optional: nil is a deployment that offers
	// none (it has no Google project, say), and then no turn is offered
	// the tools and no prompt mentions them.
	Mail  AccountTools
	Drive AccountTools
	// Nextcloud works with the account's Nextcloud files; optional in the
	// same way (a deployment that names no Nextcloud).
	Nextcloud AccountTools
	// Memory writes the memory document a turn's prompt shows. Optional,
	// and deliberately so: nil is an assembly whose turns read the memory
	// and cannot write it, which is what a turn nobody is present for is
	// given (an automation's run does not rewrite what is remembered about
	// a person). The tier that must have it checks so itself.
	Memory Remembering
	// Artifacts is the specialist that makes artifacts. Optional in the
	// same way: nil is an assembly whose turns make none, and whose prompt
	// does not offer to.
	Artifacts ArtifactMaker
	// KnowledgeBase is the specialist that changes the account's notes,
	// documents and folders. Optional in the same way: nil is an assembly
	// whose turns change nothing in the knowledge base (an automation's
	// run, which nobody is present to approve a change for).
	KnowledgeBase KnowledgeWriter

	Limits Limits
	Now    func() time.Time
	// Brand is the product's name in the system prompt.
	Brand string
}

// Check reports what is missing. Called once at start-up.
func (a Assembly) Check() error {
	switch {
	case a.Models == nil:
		return errors.New("chat: the models are not set")
	case a.Store == nil:
		return errors.New("chat: the checkpoint store is not set")
	case a.Rows == nil:
		return errors.New("chat: the prompt rows are not set")
	case a.Documents == nil:
		return errors.New("chat: the document search is not set")
	case a.Academic == nil:
		return errors.New("chat: the academic sources are not set")
	case a.Connected == nil:
		return errors.New("chat: the connected apps are not set")
	case a.Pages.Approval != tool.AlwaysAsk:
		// Held here as well as where the tool is made: a page reader that
		// could be "always allowed" is a way to send what the model has
		// read to an address the model chose, unasked.
		return errors.New("chat: the page reader is not set, or does not ask every time")
	case a.Limits.MaxSteps < 1 || a.Limits.MaxParallelTools < 1 || a.Limits.Continuations < 1:
		return fmt.Errorf("chat: the limits must all be at least 1, got %+v", a.Limits)
	case a.Now == nil:
		return errors.New("chat: Now is not set")
	case a.Brand == "":
		return errors.New("chat: the brand name is not set")
	}
	if err := a.Pages.Validate(); err != nil {
		return fmt.Errorf("chat: the page reader: %w", err)
	}
	if err := a.Models.Check(); err != nil {
		return err
	}
	if err := a.Web.Check(); err != nil {
		return err
	}
	if a.Artifacts == nil {
		return nil
	}
	// The specialist is checked with a model in place; its own is set per
	// turn.
	return a.Artifacts.Check(unsetModel{})
}

// unsetModel stands where a turn's model will be, for a start-up check of
// everything else. It is never called.
type unsetModel struct{}

func (unsetModel) Generate(context.Context, model.Request, func(model.Chunk)) (model.Response, error) {
	return model.Response{}, errors.New("chat: a turn's model was not set")
}

func (a Assembly) Build(ctx context.Context, turn Turn) (Built, error) {
	// The model first: an account whose model is gone is told so before
	// anything else is read or opened for it.
	var (
		chat      model.Model
		modelName string
		err       error
	)
	if turn.Unattended != nil && turn.Unattended.Model != "" {
		chat, modelName, err = a.Models.Named(ctx, turn.UserID, turn.Unattended.Model)
	} else {
		chat, modelName, err = a.Models.For(ctx, turn.UserID)
	}
	if err != nil {
		return Built{}, err
	}
	about, err := a.Rows.Context(ctx, turn)
	if err != nil {
		return Built{}, fmt.Errorf("chat: reading the turn's context: %w", err)
	}
	notebook := about.Notebook
	chosen, picked := picks[turn.PreferredTool]

	var tools []tool.Tool
	offered := map[string]bool{}
	// A tool the person switched off is not offered, and so is not in the
	// prompt either: every block of the prompt is written from `offered`.
	// Only the chat's own switchable tools can be named; a connected app
	// is switched off where it is connected.
	off := map[string]bool{}
	for _, name := range turn.DisabledTools {
		off[name] = true
	}
	offer := func(t tool.Tool) {
		if off[t.Name] && slices.ContainsFunc(switchable, func(s Switchable) bool { return s.Name == t.Name }) {
			return
		}
		tools = append(tools, t)
		offered[t.Name] = true
	}

	// The user's own documents, unless they limited this message to the
	// web. A notebook chat always has them and has them confined: the
	// limit is the server's, read a moment ago, and neither the request's
	// scope nor a mention can widen it.
	if turn.Scope != ScopeWeb || notebook != nil {
		scope := documents.Scope{DocumentIDs: turn.DocumentIDs, FolderIDs: turn.FolderIDs}
		if notebook != nil {
			scope.Limited, scope.Limit = true, notebook.DocumentIDs
		}
		offer(a.Documents.Tool(turn.UserID, scope))
	}

	// Outside sources, when the turn may leave the documents. A notebook
	// chat may not, whatever scope the request names (the composer's old
	// scope switch survives in a cookie); the one exception is a tool the
	// person picked for this message, which is them asking for it.
	outside := turn.Scope != ScopeDocuments
	if notebook != nil {
		outside = picked
	}
	if outside {
		// With a pick, only the picked source is offered: the person chose
		// it, and a model given five sources will not reliably use the one
		// it was asked to.
		wanted := func(name string) bool { return !picked || chosen.tool == name }
		for _, provider := range []websearch.Provider{websearch.Exa, websearch.Tavily} {
			if !wanted(provider.ToolName()) {
				continue
			}
			connected, err := a.Web.Connected(ctx, turn.UserID, provider)
			if err != nil {
				return Built{}, fmt.Errorf("chat: reading the account's web search connections: %w", err)
			}
			if connected {
				offer(a.Web.Tool(turn.UserID, provider))
			}
		}
		for _, source := range a.Academic.Tools() {
			if wanted(source.Name) {
				offer(source)
			}
		}
		// The page reader goes with the web and not with a pick: a person
		// who picked PubMed asked for PubMed.
		if !picked && notebook == nil {
			offer(a.Pages)
		}
	}

	// The memory the prompt shows, to write as well as read: the asker's
	// own, which is the document personBlock shows. Every kind of chat has
	// it, a notebook's included: remembering who is asking is not a source.
	activities := map[string]Activity{}
	if a.Memory != nil {
		activities[MemoryToolName] = Activity{Kind: "memory.personal", Active: "Updating your memory", Completed: "Updated your memory", Category: "action", Icon: "brain"}
		offer(a.Memory.Tool(MemoryTarget{UserID: turn.UserID, Shown: about.UserMemory}))
	}

	// The account's mail and its Google Drive, where connected apps are
	// offered and for the same reason: every call that reaches outside
	// asks first, and asking is the control. A notebook chat answers from
	// its sources and has neither. They are offered before the connected
	// apps, so that an app with a tool of the same name is the one left
	// out: the prompt describes these.
	mail, drive, cloud := false, false, false
	if notebook == nil && !picked {
		for _, kind := range []struct {
			tools      AccountTools
			offered    *bool
			what       string
			activities map[string]Activity
		}{
			{a.Mail, &mail, "mailboxes", mailActivities},
			{a.Drive, &drive, "Google Drive connection", driveActivities},
			{a.Nextcloud, &cloud, "Nextcloud connection", nextcloudActivities},
		} {
			if kind.tools == nil {
				continue
			}
			connected, err := kind.tools.Connected(ctx, turn.UserID)
			if err != nil {
				return Built{}, fmt.Errorf("chat: reading the account's %s: %w", kind.what, err)
			}
			if !connected {
				continue
			}
			for _, t := range kind.tools.ForTurn(turn.UserID) {
				offer(t)
				activities[t.Name] = kind.activities[t.Name]
				*kind.offered = true
			}
		}
	}

	// Connected apps, in every scope of a plain chat, the documents-only
	// one included: each call asks first, and asking is the control. (They
	// used to be denied outright in the default scope, so they were never
	// called at all.) A notebook chat has none: it answers from its
	// sources.
	release := func() {}
	var apps ConnectedTools
	connected := 0
	if notebook == nil && !picked {
		if apps, err = a.Connected.ForTurn(ctx, turn.UserID); err != nil {
			return Built{}, fmt.Errorf("chat: reading the account's connected apps: %w", err)
		}
		release = apps.Close
		for _, t := range apps.Tools() {
			if offered[t.Name] {
				// A connected app's tool with the name of one of the
				// chat's own is left out: the chat's own is the one the
				// prompt describes, and two tools of one name cannot both
				// be offered.
				continue
			}
			offer(t)
			app, _ := apps.App(t.Name)
			activities[t.Name] = ConnectedActivity(app)
			connected++
		}
	}
	// From here on every failure gives back what was opened.
	fail := func(err error) (Built, error) {
		release()
		return Built{}, err
	}

	var delegates []agent.Delegate
	artifacts := ""
	if a.Artifacts != nil {
		specialist, err := a.Artifacts.Delegate(chat, turn.UserID, turn.ThreadID)
		if err != nil {
			return fail(err)
		}
		delegates, artifacts = []agent.Delegate{specialist}, a.Artifacts.Prompt()
	}
	// The knowledge-base specialist, in every chat but one with a source
	// picked (the person asked for that source). In a notebook's chat it
	// files what it writes in the notebook and reads only what the chat may
	// read. Its changes ask first, by the same "always allow" rules as the
	// main agent's tools.
	knowledge := ""
	if a.KnowledgeBase != nil && !picked {
		writer, err := a.KnowledgeBase.Delegate(chat, KnowledgeOwner{
			UserID: turn.UserID, ThreadID: turn.ThreadID, Store: a.Store, Notebook: notebook,
			Trusted: func(ctx context.Context, call tool.Call) (bool, error) {
				if turn.Unattended != nil {
					return turn.Unattended.TrustAskFirst, nil
				}
				return a.Rows.Trusted(ctx, turn.UserID, call.Name)
			},
		})
		if err != nil {
			return fail(err)
		}
		delegates, knowledge = append(delegates, writer), a.KnowledgeBase.Prompt()
		for name, activity := range a.KnowledgeBase.Activities() {
			activities[name] = activity
		}
	}

	// An answer the model's output cap cut short is carried on instead of
	// being handed over half-finished.
	carryOn, err := agent.ContinueCutOff(a.Limits.Continuations)
	if err != nil {
		return fail(err)
	}
	system := systemPrompt(promptInput{
		Brand: a.Brand, Today: a.Now().UTC(), Context: about, Picked: picked,
		Documents: offered[documents.ToolName], Pages: offered[a.Pages.Name], Apps: connected > 0, Mail: mail, Drive: drive, Nextcloud: cloud, Offered: offered,
		Artifacts: artifacts, Memory: a.Memory, KnowledgeBase: knowledge,
	})
	built, err := agent.New(agent.Config{
		Model:            chat,
		System:           system,
		Tools:            tools,
		Delegates:        delegates,
		MaxSteps:         a.Limits.MaxSteps,
		MaxParallelTools: a.Limits.MaxParallelTools,
		Store:            a.Store,
		ModelHooks:       []agent.ModelHook{carryOn},
		// Asked at each decision and remembered nowhere: a rule removed in
		// settings is gone from the very next call. A connected app's tool
		// is trusted on its connection's row, the chat's own tools in the
		// permission rules; each is asked of the place that holds it.
		Trusted: func(ctx context.Context, call tool.Call) (bool, error) {
			if turn.Unattended != nil {
				return turn.Unattended.TrustAskFirst, nil
			}
			if apps != nil {
				if trusted, mine, err := apps.Trusted(ctx, call.Name); mine || err != nil {
					return trusted, err
				}
			}
			return a.Rows.Trusted(ctx, turn.UserID, call.Name)
		},
	})
	if err != nil {
		return fail(err)
	}
	return Built{
		Agent: built, Chat: chat, Model: modelName, Preface: Preface(turn.PreferredTool, offered),
		Activities: activities, Close: release,
		Trust: func(ctx context.Context, name string) error {
			if apps != nil {
				if _, mine := apps.App(name); mine {
					return apps.Trust(ctx, name)
				}
			}
			return a.Rows.Trust(ctx, turn.UserID, name)
		},
	}, nil
}

// ConnectedActivity is the step display's line for a connected app's tool.
// The app is named, as the account named it; the tool is not, because its
// name was chosen by a remote server.
func ConnectedActivity(app string) Activity {
	a := Activity{Kind: "connector.action", Active: "Using a connected app", Completed: "Used a connected app", Category: "connector", Icon: "connector", IntegrationSource: "mcp"}
	if app = oneLine(app, 60); app != "" {
		a.Active, a.Completed, a.IntegrationName = "Using "+app, "Used "+app, app
	}
	return a
}

// Preface is the note put before the person's message when they picked a
// source. A pick that cannot be honoured is said, not hidden: the model is
// told to tell them, so the answer does not quietly come from elsewhere.
func Preface(preferred string, offered map[string]bool) string {
	chosen, picked := picks[preferred]
	if !picked {
		return ""
	}
	if !offered[chosen.tool] {
		return "<tool_preference>\nFor this message the user chose the " + chosen.label + " tool, but it is not connected for their account. Tell them so in one sentence, and that it is set up in the account's connections. Then answer as best you can from what you do have.\n</tool_preference>\n\n"
	}
	return "<tool_preference>\nFor this message the user chose the " + chosen.label + " tool. Use " + chosen.tool + " to gather what the request needs before answering, even if another source could also work. If it finds nothing, say so, then answer as best you can.\n</tool_preference>\n\n"
}

type promptInput struct {
	Brand string
	Today time.Time
	// Context is what the database says about the turn: the account's
	// instructions, the asker, the memory, the notebook.
	Context TurnContext
	// Picked says the person picked a tool for this message.
	Picked bool
	// Documents, Pages, Apps, Mail, Drive and Nextcloud say which kinds of
	// tool the turn has.
	Documents bool
	Pages     bool
	Apps      bool
	Mail      bool
	Drive     bool
	Nextcloud bool
	Offered   map[string]bool
	// Artifacts is the body of the <artifacts> block, or "" for a turn
	// with no specialist; Memory words the memory's writing, or is nil for
	// a turn that cannot write it.
	Artifacts string
	Memory    Remembering
	// KnowledgeBase is the body of the <knowledge_base> block, or "" for a
	// turn that cannot change the knowledge base.
	KnowledgeBase string
}

// maxInstructionChars bounds the account's own instructions in the prompt.
const maxInstructionChars = 4000

// pagesPrompt tells the model what reading a page costs the person: a card
// to approve, every time.
const pagesPrompt = "You can read web pages the user points at, or that a search found and the answer needs in full. Reading a page sends a request to its address, so each call is shown to the user for approval first: read a page when the answer needs it, not to browse."

// systemPrompt builds the main agent's system prompt for one turn. It is
// sent with every model call and stored nowhere, so today's date is today's,
// an account that edits its instructions sees them on its next message,
// and a memory or a notebook's sources are never an old copy.
func systemPrompt(in promptInput) string {
	var b strings.Builder
	b.WriteString("You are " + in.Brand + "'s assistant: a study and research partner that answers from what the sources show, not from what you assume.\nToday (UTC): " + in.Today.Format("2006-01-02") + "\n\n")
	b.WriteString(`<core_behavior>
Be concise and direct. No preamble, and do not narrate what you are about to do: act, then state the outcome.
If a request is ambiguous, ask before acting. Prioritise accuracy over agreement.
Persist until the task is done or you are genuinely blocked.
</core_behavior>

`)
	if in.Documents {
		b.WriteString(`<grounding>
For any factual or informational question, search the user's documents with search_knowledge_base before answering, and answer from the passages it returns. Search again with different words when the first results do not answer the question.
If the documents do not hold enough to answer, say so plainly and ask: "Would you like me to answer from my general knowledge instead?" Answer from general knowledge only after a clear yes, and say that you are doing so.
This does not apply to casual conversation, questions about this conversation, or rewriting and formatting text the user gave you.
</grounding>

`)
	}
	var sources []string
	for _, name := range []string{websearch.Exa.ToolName(), websearch.Tavily.ToolName(), academic.PubMedTool, academic.ArxivTool, academic.PreprintTool} {
		if in.Offered[name] {
			sources = append(sources, name)
		}
	}
	if len(sources) > 0 {
		b.WriteString("<outside_sources>\nThis message may also use: " + strings.Join(sources, ", ") + ". Each is a different source; use the one that fits the question, and say which kind of source a claim rests on when it matters (a preprint is not peer reviewed).\n</outside_sources>\n\n")
	}
	if in.Pages {
		b.WriteString("<reading_pages>\n" + pagesPrompt + "\n</reading_pages>\n\n")
	}
	if in.Apps {
		b.WriteString("<connected_apps>\nSome of your tools belong to apps the user connected. Use one when the request is about that app's data or needs its action. Each call is shown to the user for approval before it runs: call the tool directly, and do not ask for permission in prose first.\n</connected_apps>\n\n")
	}
	if in.Mail {
		b.WriteString("<email>\n" + mailPrompt + "\n</email>\n\n")
	}
	if in.Drive {
		b.WriteString("<google_drive>\n" + drivePrompt + "\n</google_drive>\n\n")
	}
	if in.Nextcloud {
		b.WriteString("<nextcloud>\n" + nextcloudPrompt + "\n</nextcloud>\n\n")
	}
	if in.KnowledgeBase != "" {
		b.WriteString("<knowledge_base>\n" + in.KnowledgeBase + "\n</knowledge_base>\n\n")
	}
	if in.Artifacts != "" {
		b.WriteString("<artifacts>\n" + in.Artifacts + "\n</artifacts>\n\n")
	}
	b.WriteString(personBlock(in.Context, in.Memory))
	b.WriteString(notebookBlock(in.Context.Notebook, in.Picked))
	if in.Context.Citations {
		b.WriteString(`<citations>
Search results label each source [n]. Cite with that label and nothing else:
1. Put the label right after the claim it supports.
2. Several sources for one claim: stack the brackets, [1][2].
3. Copy labels exactly as shown. Never renumber them or invent one.
4. Write the bare [n] only: no links, no footnotes, no "References" section.
5. Only cite what a shown source supports. If nothing shown backs a claim, leave it uncited.
</citations>

`)
	} else {
		b.WriteString("<citations>\nThe user has citations switched off. Do not write [n] labels.\n</citations>\n\n")
	}
	b.WriteString(`<output_format>
Write mathematics in LaTeX. Never show tool names, parameter names or ids to the user.
</output_format>
`)
	if text := strings.TrimSpace(in.Context.Instructions); text != "" {
		// The instructions are a person's text, so they are bounded and
		// made inert like every other block a person wrote.
		text = inert(clipped(text, maxInstructionChars))
		b.WriteString("\n<user_instructions>\nThe user set these standing instructions for every chat. Follow them where they do not conflict with the rules above.\n" + text + "\n</user_instructions>\n")
	}
	return b.String()
}

// Activity is how one tool's work is worded and drawn in a turn's step
// display.
type Activity struct {
	// Kind is a stable name for the kind of work, for the page's grouping.
	Kind string
	// Active and Completed are the line while the tool runs and after.
	Active, Completed string
	// Category and Icon pick the drawing: Category is one of file, research,
	// artifact, connector, action.
	Category, Icon string
	// Integration names the service the work is done with, when the page
	// has a logo for it: Source is native, connector or mcp.
	IntegrationSource, IntegrationKey, IntegrationName string
}
