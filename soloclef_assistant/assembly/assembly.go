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
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/academic"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/addsources"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/automationdrafts"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/documents"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/figuretool"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/mailtools"
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
	// DisabledTools are the tools the person switched off in the
	// composer's tool list (Menu): any tool a turn could be offered, the
	// connected ones included.
	DisabledTools []string `json:"disabled_tools,omitempty"`
	// TimeZone is the IANA zone of the person's device, as the chat request
	// said it (checked there), or "". It is saved with a paused turn so the
	// resumed one drafts schedules in the same zone.
	TimeZone string `json:"time_zone,omitempty"`
	// Unattended is set for a turn nobody is present for (an automation's
	// run) and nil for a chat's. It is never read from a request or from a
	// saved turn, which is why it has no JSON name: only the code that
	// starts such a turn can set it.
	Unattended *Unattended `json:"-"`
}

// Unattended is the trust an unattended turn runs under. It replaces the
// account's "ask first" marks: those are about a chat the person watches,
// and a run nobody watches could not ask them, so it goes by the tools' own
// declarations (tool.Approval) and the automation's one rule.
type Unattended struct {
	// TrustAskFirst lets a tool declared tool.Ask run without asking. A
	// tool declared tool.AlwaysAsk is not affected by it: the agent loop
	// never consults trust for one, so it waits, and the runner refuses it.
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
	{Name: addsources.ToolName, Description: "Add papers and web pages to the notebook's sources"},
	{Name: websearch.Exa.ToolName(), Description: "Search the web with Exa, when you have connected it"},
	{Name: websearch.Tavily.ToolName(), Description: "Search the web with Tavily, when you have connected it"},
	{Name: academic.PubMedTool, Description: "Search PubMed, with full text from PubMed Central"},
	{Name: academic.ArxivTool, Description: "Search arXiv"},
	{Name: academic.PreprintTool, Description: "Search bioRxiv and medRxiv preprints"},
	{Name: pagereader.ToolName, Description: "Read a web page in full, asking first"},
	{Name: MemoryToolName, Description: "Remember durable facts and preferences between conversations"},
	{Name: figuretool.ToolName, Description: "Draw charts and diagrams in answers"},
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
	// StopAsking takes a tool of this turn off the person's "ask first"
	// list, which is what an "always allow" answer on its approval card
	// means. It refuses for an unattended turn: nobody answered a card.
	StopAsking func(ctx context.Context, tool string) error
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
	// AsksFirst reports whether the account wants to be asked before the
	// tool runs. Every tool runs without asking unless the person said so.
	AsksFirst(ctx context.Context, userID string, tool MarkedTool) (bool, error)
	// StopAsking takes the tool off the account's "ask first" list. A tool
	// that is not on it is no error: the answer is the same either way.
	StopAsking(ctx context.Context, userID string, tool MarkedTool) error
}

// MarkedTool names a tool the way an "ask first" mark is kept: one of the
// chat's own tools by its name, a connected app's by its connection and the
// server's own name for it. The model's name for an app's tool changes when
// another app with a tool of that name is connected, and a mark must not
// move with it.
type MarkedTool struct {
	// Connector is the connected app's connection, or 0 for one of the
	// chat's own tools (connections are numbered from 1).
	Connector int64
	Name      string
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
	// Marked is how a mark on the tool the model knows as name is kept.
	// mine is false for a name that is not one of these tools.
	Marked(name string) (tool MarkedTool, mine bool)
	// App names the connected app a tool belongs to, for the step display.
	App(name string) (app string, mine bool)
	Close()
}

// AccountTools are tools made from something the account itself connected
// under Connectors: its mailboxes (mailtools), its Google Drive
// (drivetools), its Nextcloud (nextcloudtools). Unlike a connected app they are this product's own tools,
// so the prompt describes them; like one, they exist only for an account
// that connected the thing.
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
	// notebookID is the notebook whose Studio lists what it makes: a
	// notebook chat's own, 0 for any other turn (Build says which).
	Delegate(chat model.Model, userID string, threadID, notebookID int64) (agent.Delegate, error)
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
	// Automations drafts automations for a card in the chat
	// (create_automation). Optional in the same way: nil is an assembly
	// whose turns draft none (an automation's own run, which nobody is
	// present to read a card in).
	Automations *automationdrafts.Tools
	// Sources adds papers and pages to a notebook's sources (add_sources):
	// the API tier's research import. Optional in the same way: nil is an
	// assembly whose turns add none (the worker's, which has no import).
	Sources addsources.Adder

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
		// Held here as well as where the tool is made: a page reader
		// declared otherwise would let an automation's unattended run send
		// what the model has read to an address the model chose. (A chat
		// goes by the person's own marks, which they chose knowingly.)
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
	if a.Automations != nil {
		if err := a.Automations.Check(); err != nil {
			return err
		}
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
	// The sources this turn adds to its notebook, shared by the tool that
	// adds them and the search that reads them.
	joined := &documents.Joined{}
	chosen, picked := picks[turn.PreferredTool]

	var tools []tool.Tool
	offered := map[string]bool{}
	// A tool the person switched off is not offered, and so is not in the
	// prompt either: every block of the prompt is written from `offered`.
	// Any tool the composer's list shows can be switched off there (Menu),
	// a mailbox's, Drive's or a connected app's as much as the chat's own:
	// the list is how a person keeps a notebook chat to the tools they
	// want, and what a turn is not offered costs no prompt. A name that is
	// no tool of this turn switches nothing off.
	off := map[string]bool{}
	for _, name := range turn.DisabledTools {
		off[name] = true
	}
	offer := func(t tool.Tool) bool {
		if off[t.Name] {
			return false
		}
		tools = append(tools, t)
		offered[t.Name] = true
		return true
	}

	// The user's own documents, unless they limited this message to the
	// web. A notebook chat always has them and has them confined: the
	// limit is the server's, read a moment ago, and neither the request's
	// scope nor a mention can widen it.
	if turn.Scope != ScopeWeb || notebook != nil {
		scope := documents.Scope{DocumentIDs: turn.DocumentIDs, FolderIDs: turn.FolderIDs}
		if notebook != nil {
			scope.Limited, scope.Limit, scope.Joined = true, notebook.DocumentIDs, joined
		}
		offer(a.Documents.Tool(turn.UserID, scope))
	}

	// Adding papers and pages to the notebook's sources, in a notebook's
	// chat a person is present for. It is offered with a picked source too:
	// "search PubMed and add the two best" is one request, and adding is
	// not a source competing with the pick. What it adds joins the search
	// above for the rest of this turn (joined), so a paper added is
	// searchable as soon as it is indexed, not from the next message.
	addingSources := false
	if a.Sources != nil && notebook != nil && turn.Unattended == nil {
		addingSources = offer(addsources.Tool(a.Sources, turn.UserID, notebook.ID, turn.ThreadID, joined))
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
	// The scholarly sources in a notebook's chat with nothing picked
	// (Daniel, 2026-11-14: a notebook chat searches PubMed without the
	// pick). A notebook is research material, and the papers its sources
	// lack are found here and added with add_sources; the sources stay
	// what an answer is built from (the notebook block of the prompt says
	// so). Only the keyless scholarly indexes come: web search runs on the
	// account's own paid key, and the page reader is the web's, so both
	// still wait for the person to pick them. Not in a run nobody watches
	// (an automation), whose tools were chosen when it was made.
	scholarly := false
	if notebook != nil && !picked && turn.Unattended == nil {
		for _, source := range a.Academic.Tools() {
			if offer(source) {
				scholarly = true
			}
		}
	}

	// The memory the prompt shows, to write as well as read: the asker's
	// own, which is the document personBlock shows. Every kind of chat has
	// it, a notebook's included: remembering who is asking is not a source.
	activities := map[string]Activity{}
	if addingSources {
		// Completed is the line a saved step falls back to; a finished call
		// is worded from its answer ("Added 2 sources", the chat's
		// finishedLines).
		activities[addsources.ToolName] = Activity{Kind: addsources.ActivityKind, Active: "Adding sources", Completed: "Added sources",
			Category: "research", Icon: "library"}
	}
	if a.Memory != nil {
		activities[MemoryToolName] = Activity{Kind: "memory.personal", Active: "Updating your memory", Completed: "Updated your memory", Category: "action", Icon: "brain"}
		offer(a.Memory.Tool(MemoryTarget{UserID: turn.UserID, Shown: about.UserMemory}))
	}

	// The account's mail and its Google Drive, where connected apps are
	// offered. A notebook chat has them too (2026-11-13: the notebook chat
	// is the only chat the apps have, and its Tools menu offers them): the
	// notebook's sources stay its primary material, and notebookBlock tells
	// the model these are for requests about the person's mail, files or
	// apps. Only a picked source keeps them out, because the person asked
	// for that source. Like every tool they run unless the person marked
	// them "ask first". They are offered before the connected apps, so that
	// an app with a tool of the same name is the one left out: the prompt
	// describes these.
	mail, drive, cloud := false, false, false
	if !picked {
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
				if offer(t) {
					activities[t.Name] = kind.activities[t.Name]
					// The block of the prompt about a kind is written only
					// when one of its tools is offered.
					*kind.offered = true
				}
			}
		}
	}

	// Connected apps, in every scope, the documents-only one included: like
	// every tool they run unless the person marked them "ask first". (They
	// used to be denied outright in the default scope, so they were never
	// called at all.) A notebook chat has them as it has the mail, above; a
	// picked source keeps them out.
	//
	// What they cost a turn that does not use them is their definitions
	// in the prompt. That is bounded by the person, not guessed at here:
	// the composer lists each app's tools with a switch for the whole app
	// (Menu), and a switched-off tool is not offered. Loading them lazily
	// (a tool that reveals the rest) was weighed and not chosen: it adds a
	// model round trip to every question that needs one, and the agent's
	// tool list is fixed for a turn (agent.New), which is what lets a
	// paused turn resume with the same tools.
	release := func() {}
	var apps ConnectedTools
	connected := 0
	if !picked {
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
			app, _ := apps.App(t.Name)
			// Each answer is numbered and cited like a web page.
			if offer(citedApp(t, app)) {
				activities[t.Name] = ConnectedActivity(app)
				connected++
			}
		}
	}
	// Drafting an automation, in a chat a person is present for: the draft
	// is a card for them to create, and a run nobody watches has nobody to
	// press Create. A picked source keeps it out like every tool not
	// picked.
	automations := ""
	if a.Automations != nil && turn.Unattended == nil && !picked {
		if offer(a.Automations.Tool(turn.UserID)) {
			activities[automationdrafts.ToolName] = Activity{Kind: "automation.draft", Active: "Drafting an automation", Completed: "Drafted an automation for you to check", Category: "action", Icon: "workflow"}
			automations = a.Automations.Prompt(automationdrafts.Here{TimeZone: turn.TimeZone})
		}
	}

	// Figures in the answer (a chart, a diagram), in a chat a person reads:
	// an automation's run is read as text (a notification, a mail), where a
	// figure would be drawn for nobody. Offered whatever source was picked:
	// a figure is not a source, it is how the answer is shown.
	if turn.Unattended == nil {
		offer(figuretool.Tool())
	}

	// From here on every failure gives back what was opened.
	fail := func(err error) (Built, error) {
		release()
		return Built{}, err
	}

	var delegates []agent.Delegate
	artifacts := ""
	// What a notebook's chat makes is listed in that notebook's Studio as
	// well as in the chat: the chat is where a person asks for it, the
	// Studio where they find it again. A turn nobody attends (an
	// automation's step confined to a notebook) keeps its outputs in its
	// run, as before; the notebook's Studio lists what was made there.
	studio := int64(0)
	if notebook != nil && turn.Unattended == nil {
		studio = notebook.ID
	}
	if a.Artifacts != nil {
		specialist, err := a.Artifacts.Delegate(chat, turn.UserID, turn.ThreadID, studio)
		if err != nil {
			return fail(err)
		}
		delegates, artifacts = []agent.Delegate{specialist}, a.Artifacts.Prompt()
	}
	// The knowledge-base specialist, in every chat but one with a source
	// picked (the person asked for that source). In a notebook's chat it
	// files what it writes in the notebook and reads only what the chat may
	// read. Its tools go by the turn's approval policy, as the main
	// agent's do.
	policy := a.policy(turn, apps)
	knowledge := ""
	if a.KnowledgeBase != nil && !picked {
		writer, err := a.KnowledgeBase.Delegate(chat, KnowledgeOwner{
			UserID: turn.UserID, ThreadID: turn.ThreadID, Store: a.Store, Notebook: notebook,
			Policy: policy,
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
		Figures:   offered[figuretool.ToolName],
		Artifacts: artifacts, Memory: a.Memory, KnowledgeBase: knowledge, Automations: automations,
		AddingSources: addingSources, Studio: studio != 0 && artifacts != "", Scholarly: scholarly,
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
		Trusted:          policy.Trusted,
		AskFirst:         policy.AskFirst,
	})
	if err != nil {
		return fail(err)
	}
	return Built{
		Agent: built, Chat: chat, Model: modelName, Preface: Preface(turn.PreferredTool, offered),
		Activities: activities, Close: release,
		StopAsking: func(ctx context.Context, name string) error {
			if turn.Unattended != nil {
				return errors.New("chat: an unattended turn has no person to stop asking")
			}
			return a.Rows.StopAsking(ctx, turn.UserID, markedTool(apps, name))
		},
	}, nil
}

// ApprovalPolicy is which of the agent's two policies a turn runs under
// (agent.Config): exactly one of the two is set.
type ApprovalPolicy struct {
	Trusted  func(ctx context.Context, call tool.Call) (bool, error)
	AskFirst func(ctx context.Context, call tool.Call) (bool, error)
}

// policy is the turn's approval policy, for the main agent and the
// knowledge-base specialist alike.
//
// A chat goes by the person's own "ask first" marks and nothing else (the
// owner's rule, 2026-11-13: "let default be to always allow all, then
// settings you can change it"). Each mark is read at the moment a call is
// decided and remembered nowhere, so one changed in settings applies from
// the very next call. A turn nobody attends (an automation's run) goes by
// the tools' own declarations and the automation's one switch instead: a
// person's marks are about a chat they watch, and the run could not ask
// them anyway.
func (a Assembly) policy(turn Turn, apps ConnectedTools) ApprovalPolicy {
	if turn.Unattended != nil {
		trust := turn.Unattended.TrustAskFirst
		return ApprovalPolicy{Trusted: func(context.Context, tool.Call) (bool, error) { return trust, nil }}
	}
	return ApprovalPolicy{AskFirst: func(ctx context.Context, call tool.Call) (bool, error) {
		if cardIsTheQuestion[call.Name] {
			return false, nil
		}
		return a.Rows.AsksFirst(ctx, turn.UserID, markedTool(apps, call.Name))
	}}
}

// cardIsTheQuestion are the tools whose own card is the person's decision:
// the tool only prepares something (an automation draft, a mail draft) that
// the person then creates or sends, or discards, on its card. An "ask first"
// prompt before it would ask whether to show them a question, so their
// marks never apply to it (the settings page does not list it either:
// agentrules' catalogue).
var cardIsTheQuestion = map[string]bool{automationdrafts.ToolName: true, mailtools.DraftTool: true}

// markedTool is how the mark on the tool the model knows as name is kept:
// a connected app's by its connection, any other by its name.
func markedTool(apps ConnectedTools, name string) MarkedTool {
	if apps != nil {
		if marked, mine := apps.Marked(name); mine {
			return marked
		}
	}
	return MarkedTool{Name: name}
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
	// Figures says draw_figure is offered.
	Figures bool
	Offered map[string]bool
	// Artifacts is the body of the <artifacts> block, or "" for a turn
	// with no specialist; Memory words the memory's writing, or is nil for
	// a turn that cannot write it.
	Artifacts string
	Memory    Remembering
	// KnowledgeBase is the body of the <knowledge_base> block, or "" for a
	// turn that cannot change the knowledge base.
	KnowledgeBase string
	// Automations is the body of the <automations> block, or "" for a turn
	// that cannot draft one.
	Automations string
	// AddingSources says the turn can add sources to its notebook
	// (add_sources); Studio says what it makes is listed in the notebook's
	// Studio.
	AddingSources bool
	Studio        bool
	// Scholarly says a notebook's chat has the scholarly searches without
	// a pick.
	Scholarly bool
}

// maxInstructionChars bounds the account's own instructions in the prompt.
const maxInstructionChars = 4000

// pagesPrompt tells the model what reading a page is: a request to an
// address, which the person may have asked to approve each time.
const pagesPrompt = "You can read web pages the user points at, or that a search found and the answer needs in full. Reading a page sends a request to its address: read a page when the answer needs it, not to browse."

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
		b.WriteString("<connected_apps>\nSome of your tools belong to apps the user connected. Use one when the request is about that app's data or needs its action. A call is shown to the user for approval first only if they asked for that tool to be: call the tool directly, and do not ask for permission in prose first.\n</connected_apps>\n\n")
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
	if in.Figures {
		b.WriteString("<figures>\n" + figuretool.Guidance + "\n</figures>\n\n")
	}
	if in.KnowledgeBase != "" {
		b.WriteString("<knowledge_base>\n" + in.KnowledgeBase + "\n</knowledge_base>\n\n")
	}
	if in.Automations != "" {
		b.WriteString("<automations>\n" + in.Automations + "</automations>\n\n")
	}
	if in.Artifacts != "" {
		b.WriteString("<artifacts>\n" + in.Artifacts + "\n</artifacts>\n\n")
	}
	if in.AddingSources {
		b.WriteString("<adding_sources>\n" + addsources.Prompt() + "\n</adding_sources>\n\n")
	}
	b.WriteString(personBlock(in.Context, in.Memory))
	b.WriteString(notebookBlock(in.Context.Notebook, notebookAbilities{
		pickedTool: in.Picked, connected: in.Apps || in.Mail || in.Drive || in.Nextcloud,
		addsSources: in.AddingSources, studio: in.Studio, scholarly: in.Scholarly,
	}))
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
