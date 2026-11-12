package chat

import (
	"context"
	"errors"
	"fmt"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint/postgres"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/openaicompat"
	"git.danvics.com/danvics/soloclef/soloclef_agent/uistream"
	filestore "git.danvics.com/danvics/soloclef/soloclef_filestore"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/celeryqueue"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/chat/memory"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/chat/threads"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/chat/turns"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/deliverablejobs"
	storeddocuments "git.danvics.com/danvics/soloclef/soloclef_api/internal/documents"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/knowledgebase"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/podcasts"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/routeanalytics"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/deliverables"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/fileread"
	"git.danvics.com/danvics/soloclef/soloclef_studio/sandbox"
)

// Settings is what a deployment says about its chat. Every field without
// "optional" in its comment is required, and NewService reports all that are
// missing at once: a chat that starts half-configured answers its first
// question with an error nobody is watching for.
//
// There are no defaults for anything that names a server, a model or a
// limit. A default model name is a model nobody chose; a default address is
// a request sent somewhere nobody meant.
type Settings struct {
	// ModelURL is the model proxy's OpenAI-compatible root, ending in /v1.
	// It is the only way this tier reaches a model: chat, titles and query
	// embeddings all go through it, and it alone holds provider credentials.
	ModelURL string
	// ModelKey is the proxy key.
	ModelKey string
	// ChatModel is the proxy's name for the model that answers an account
	// which has chosen none of its own (models.go). It is held to the
	// proxy's list like any other name: if the proxy does not offer it,
	// such an account is told so instead of being answered.
	ChatModel string

	// EmbeddingModel is the proxy's name for the model the documents were
	// indexed with, and EmbeddingDimension the length of its vectors.
	EmbeddingModel     string
	EmbeddingDimension int

	// The vector store the indexer writes to.
	QdrantURL string
	// QdrantAPIKey is optional: a server without one takes none.
	QdrantAPIKey     string
	QdrantCollection string
	QdrantHNSWEf     int
	// QdrantQuantized says the collection stores quantized vectors; then
	// QdrantRescore and QdrantOversampling say how a search uses them.
	QdrantQuantized    bool
	QdrantRescore      bool
	QdrantOversampling float64

	// NCBITool names this deployment to PubMed, as NCBI asks.
	NCBITool string
	// NCBIEmail and NCBIAPIKey are optional.
	NCBIEmail  string
	NCBIAPIKey string

	// ValkeyURL is where running turns are coordinated across processes.
	ValkeyURL string

	// Brand is the product's name in the system prompt.
	Brand string

	// MaxSteps is how many steps one turn may take; MaxTurn how long.
	MaxSteps int
	MaxTurn  time.Duration

	// ImageModel is the proxy's name for the model that draws images and
	// infographics. Empty means this deployment draws none, and a person
	// who asks for one is told so; no other model is tried.
	ImageModel string
	// VisionModel is the proxy's name for the model that looks at pictures
	// for an account that chose none: a fetched file's pictures, and an
	// infographic read back to check its text. Empty means neither is
	// looked at for such an account. A built document is measured, never
	// looked at (deliverables, layout_rules.go).
	VisionModel string
	// SandboxEnabled says reports, slides, spreadsheets and interactive
	// pages are built, in the sandbox Sandbox describes. Off, a person who
	// asks for one is told it is not available here.
	SandboxEnabled bool
	Sandbox        sandbox.Settings

	// FrontendURL is the web app's address, which a public link to a thread
	// starts with. Optional: without it a link's address is a path.
	FrontendURL string

	// SecretKey is the deployment's secret key: what the credentials of an
	// account's mailboxes and Drive grant are sealed with, and so what the
	// chat's mail and Drive tools open them with.
	SecretKey string
	// MicrosoftMail and GoogleDrive are the deployment's Microsoft app
	// registration and Google project, each optional as a whole: without
	// the first a Microsoft mailbox says Microsoft mail is not set up
	// here, and without the second no chat is offered the Drive tools.
	MicrosoftMail assembly.MicrosoftMailSettings
	GoogleDrive   assembly.GoogleDriveSettings
	// NextcloudURL is the deployment's Nextcloud, the one server a stored
	// app password is sent to. Optional: without it no chat is offered the
	// Nextcloud tools.
	NextcloudURL string
}

func (s Settings) check() error {
	var missing []string
	need := func(name string, set bool) {
		if !set {
			missing = append(missing, name)
		}
	}
	need("ModelURL", strings.TrimSpace(s.ModelURL) != "")
	need("ModelKey", s.ModelKey != "")
	need("ChatModel", s.ChatModel != "")
	need("EmbeddingModel", s.EmbeddingModel != "")
	need("EmbeddingDimension", s.EmbeddingDimension > 0)
	need("QdrantURL", strings.TrimSpace(s.QdrantURL) != "")
	need("QdrantCollection", s.QdrantCollection != "")
	need("QdrantHNSWEf", s.QdrantHNSWEf > 0)
	need("QdrantOversampling (when QdrantQuantized)", !s.QdrantQuantized || s.QdrantOversampling >= 1)
	need("NCBITool", strings.TrimSpace(s.NCBITool) != "")
	need("ValkeyURL", s.ValkeyURL != "")
	need("Brand", s.Brand != "")
	need("MaxSteps", s.MaxSteps > 0)
	need("MaxTurn", s.MaxTurn > 0)
	need("SecretKey", s.SecretKey != "")
	if len(missing) > 0 {
		return fmt.Errorf("chat: these settings are missing or unusable: %s", strings.Join(missing, ", "))
	}
	return nil
}

// Deps is what the process already has and the chat shares.
type Deps struct {
	Pool *pgxpool.Pool
	// Files is where a saved artifact's file is written.
	Files filestore.Writer
	// Queue is where a saved artifact's indexing is queued.
	Queue celeryqueue.Enqueuer
	// HTTP is the client for the model proxy, the vector store and the
	// outside sources. It must not have a whole-request timeout: an answer
	// streams for longer than any timeout that would suit a search. Each
	// call is bounded by its turn's context instead.
	HTTP *http.Client
	// Voices is the podcast voice catalogue the podcast routes use: a
	// podcast asked for in chat is opened with voices proposed from it.
	Voices podcasts.Catalogue
	// Events records "chat_created". The zero value records nothing: a
	// deployment with no analytics key.
	Events routeanalytics.Capture
	Logger *slog.Logger
	// Lifetime is cancelled when the server shuts down.
	Lifetime context.Context
	// KnowledgeBase is what the chat's knowledge-base specialist changes
	// the account's notes, documents and folders with: the same value the
	// undo routes are given, so a change and its undo index and remove in
	// one way.
	KnowledgeBase knowledgebase.Changes
	// Parsers is the deployment's parser registry: what an email
	// attachment, a Drive file or a Nextcloud file is read with in a chat
	// (assembly.ToolSettings.Parsers).
	Parsers fileread.Parsers
}

// The choices below are the chat's own, not a deployment's: they are how
// this code is meant to run, they are named, and each says why it is what
// it is. A deployment that needs one changed changes it here, in review.
const (
	// A turn's lease outlives five missed heartbeats, so one slow round
	// trip to Valkey cannot cost a healthy turn its thread, and a process
	// that dies frees its threads within a minute.
	leaseLifetime  = 60 * time.Second
	leaseHeartbeat = 10 * time.Second

	// Proxies commonly drop a connection idle for 60 seconds; a comment
	// line every 15 keeps the stream well inside that.
	keepAliveEvery = 15 * time.Second
	// What a page that reloads mid-answer may be behind by.
	saveEvery = 3 * time.Second
	// A title is six words; a model that has not produced one in twenty
	// seconds is not going to.
	titleTimeout = 20 * time.Second
	// A reader that accepts nothing for a minute has gone.
	stallTimeout = 60 * time.Second

	// Eight searches at once is more than a model asks for in one batch and
	// few enough not to matter to the stores behind them.
	maxParallelTools = 8
	// An answer cut off by the output cap twice over is a runaway, not a
	// long answer.
	continuations = 2

	// The proxy's model list is read at most this often per process, and a
	// read may take this long. Thirty seconds is how long a model removed
	// from the proxy can still pass the check at the start of a turn (the
	// proxy then refuses the call, and the person is told the same thing).
	modelListFresh   = 30 * time.Second
	modelListTimeout = 10 * time.Second

	// A thread is summarised when its stored conversation passes 240,000
	// characters, about 60,000 tokens: well inside every chat model's
	// context, and already several times what a question costs when the
	// thread is new. The most recent 60,000 characters are kept word for
	// word, so a summary is written once per many turns and not on each.
	// An old tool result is shown to the summariser up to 1,500 characters:
	// enough to say what was found.
	summariseAbove     = 240_000
	summaryKeep        = 60_000
	summaryResultChars = 1_500

	// A stopped turn lets go of its thread within a heartbeat or two; a
	// delete that has waited five seconds tells the caller to try again.
	threadStopWait = 5 * time.Second

	// A model call that failed before any output is tried three times in
	// all, a second apart at first.
	modelAttempts   = 3
	modelBackoff    = time.Second
	modelMaxBackoff = 10 * time.Second

	// A person's own model endpoint gets ten seconds to accept a
	// connection; its answer then streams for as long as it is long.
	ownModelConnectTimeout = 10 * time.Second
)

// NewService builds the chat for a server: the model, the tools, the specialist,
// the coordination of turns, and the Service the routes run on.
//
// It creates the loop's checkpoint table if it is not there. The returned
// function releases what it opened and must be called at shutdown, after
// Service.Wait.
func NewService(ctx context.Context, settings Settings, deps Deps) (*Service, func(), error) {
	if err := settings.check(); err != nil {
		return nil, nil, err
	}
	switch {
	case deps.Pool == nil:
		return nil, nil, errors.New("chat: the database pool is not set")
	case deps.Files == nil:
		return nil, nil, errors.New("chat: the file store is not set")
	case deps.Queue == nil:
		return nil, nil, errors.New("chat: the queue is not set")
	case deps.HTTP == nil:
		return nil, nil, errors.New("chat: the HTTP client is not set")
	case deps.HTTP.Timeout != 0:
		return nil, nil, fmt.Errorf("chat: the HTTP client has a %s timeout, which would cut every answer that streams for longer", deps.HTTP.Timeout)
	case deps.Voices == nil:
		return nil, nil, errors.New("chat: the podcast voice catalogue is not set")
	case deps.Logger == nil:
		return nil, nil, errors.New("chat: the logger is not set")
	case deps.Lifetime == nil:
		return nil, nil, errors.New("chat: the lifetime context is not set")
	}
	knowledge := knowledgebase.Writer{Changes: deps.KnowledgeBase}
	if err := knowledge.Check(); err != nil {
		return nil, nil, err
	}

	// open builds the client for one proxy model name. It is called per
	// turn, with the turn's account's model: a client is an address, a
	// key and a name, so there is nothing to keep between turns.
	open := func(name string) (model.Model, error) {
		client, err := openaicompat.New(openaicompat.Config{
			BaseURL: settings.ModelURL, APIKey: settings.ModelKey, Model: name, HTTPClient: deps.HTTP,
		})
		if err != nil {
			return nil, err
		}
		return model.WithRetry(client, model.RetryPolicy{Attempts: modelAttempts, Backoff: modelBackoff, MaxBackoff: modelMaxBackoff})
	}
	// Built once here so that a wrong address or key fails the start and
	// not the first question.
	if _, err := open(settings.ChatModel); err != nil {
		return nil, nil, err
	}
	choices := StoredChoices{Pool: deps.Pool}
	catalogue := &Catalogue{
		URL: settings.ModelURL, Key: settings.ModelKey, HTTP: deps.HTTP,
		Timeout: modelListTimeout, Fresh: modelListFresh, Now: time.Now,
		// The one name on the proxy this tier knows is not a chat model.
		NotChat: notChat(settings),
	}
	if err := catalogue.Check(); err != nil {
		return nil, nil, err
	}
	// An account's own OpenAI-compatible connections, whose models join the
	// menu and are called directly at the address the person gave, through
	// the egress guard (soloclef_assistant/ownmodels). Their keys were
	// sealed under this same secret by internal/modelconnections.
	own, err := ownmodels.ForDeployment(deps.Pool, settings.SecretKey, ownModelConnectTimeout,
		model.RetryPolicy{Attempts: modelAttempts, Backoff: modelBackoff, MaxBackoff: modelMaxBackoff})
	if err != nil {
		return nil, nil, err
	}
	models := &Models{Catalogue: catalogue, Choices: choices, Default: settings.ChatModel, Open: open, Own: own}

	checkpoints, err := postgres.New(deps.Pool)
	if err != nil {
		return nil, nil, err
	}

	// The tools every tier that runs a turn builds the same way
	// (assembly.NewTools): the document search, the academic sources, the
	// page reader, the web searches and the account's connected apps.
	tools, closeTools, err := assembly.NewTools(assembly.ToolSettings{
		ModelURL: settings.ModelURL, ModelKey: settings.ModelKey,
		EmbeddingModel: settings.EmbeddingModel, EmbeddingDimension: settings.EmbeddingDimension,
		QdrantURL: settings.QdrantURL, QdrantAPIKey: settings.QdrantAPIKey, QdrantCollection: settings.QdrantCollection,
		QdrantHNSWEf: settings.QdrantHNSWEf, QdrantQuantized: settings.QdrantQuantized,
		QdrantRescore: settings.QdrantRescore, QdrantOversampling: settings.QdrantOversampling,
		NCBITool: settings.NCBITool, NCBIEmail: settings.NCBIEmail, NCBIAPIKey: settings.NCBIAPIKey,
		ValkeyURL: settings.ValkeyURL, Brand: settings.Brand,
		SecretKey: settings.SecretKey, MicrosoftMail: settings.MicrosoftMail, GoogleDrive: settings.GoogleDrive,
		NextcloudURL: settings.NextcloudURL,
		// What a file a tool fetches is read with: the parsers uploads are
		// read with, and the vision model a picture is described by for an
		// account that chose none.
		Parsers: deps.Parsers, VisionModel: settings.VisionModel,
		// The names this tier offers beyond the assembly's own, so that a
		// connected app's tool of the same name is given another.
		ReservedNames: []string{deliverables.DelegateName, memory.ToolName},
	}, assembly.ToolDeps{Pool: deps.Pool, HTTP: deps.HTTP, Logger: deps.Logger, Own: own})
	if err != nil {
		return nil, nil, err
	}
	searcher, sources, pages, pageTool := tools.Documents, tools.Academic, tools.Pages, tools.PageTool

	rows := Store{Pool: deps.Pool}
	remembering := memory.Writer{Pool: deps.Pool, Logger: deps.Logger}
	if err := remembering.Check(); err != nil {
		closeTools()
		return nil, nil, err
	}
	// The artifact specialist, built the way the worker builds it for an
	// automation's run (deliverables.NewSpecialist): each optional part is
	// the configured one or the type that says this deployment has none.
	specialist, forgetSandbox, err := deliverables.NewSpecialist(deliverables.Settings{
		ModelURL: settings.ModelURL, ModelKey: settings.ModelKey,
		ImageModel: settings.ImageModel, VisionModel: settings.VisionModel,
		SandboxEnabled: settings.SandboxEnabled, Sandbox: settings.Sandbox,
	}, deliverables.Parts{
		Pool: deps.Pool, Store: checkpoints, Files: deps.Files, HTTP: deps.HTTP, Voices: deps.Voices, Logger: deps.Logger,
		// An account's own vision model, for reading an infographic back.
		Own:          own,
		PublishVideo: deliverablejobs.PublishVideo(deps.Queue, TurnPath),
		// The Go indexer's own job, the one a restored version's text is
		// indexed with.
		Index: func(ctx context.Context, documentID int64, userID string) error {
			_, err := deps.Queue.Enqueue(ctx, storeddocuments.ReindexOf(documentID, celeryqueue.LogContextFor(TurnPath, "", userID)))
			return err
		},
	})
	if err != nil {
		closeTools()
		return nil, nil, err
	}
	assembly := Assembly{
		Models: models, Store: checkpoints, Rows: rows,
		Documents: searcher,
		Web:       tools.Web,
		Academic:  sources,
		Pages:     pageTool,
		Connected: tools.Connected,
		Mail:      tools.Mail,
		Drive:     tools.Drive,
		Nextcloud: tools.Nextcloud,
		Memory:    remembering,
		Artifacts: ArtifactSpecialist{Specialist: specialist},
		// The specialist that changes the account's documents and folders,
		// each change asked for and recorded so it can be undone.
		KnowledgeBase: knowledge,
		Limits:        Limits{MaxSteps: settings.MaxSteps, MaxParallelTools: maxParallelTools, Continuations: continuations},
		Now:           time.Now,
		Brand:         settings.Brand,
	}
	if err := assembly.Check(); err != nil {
		closeTools()
		return nil, nil, err
	}

	coordinator, err := turns.NewValkey(settings.ValkeyURL)
	if err != nil {
		closeTools()
		return nil, nil, err
	}
	// What this function opened, closed together.
	closeAll := func() {
		coordinator.Close()
		closeTools()
	}
	registry, err := turns.New(turns.Config{
		Store: coordinator, Lifetime: leaseLifetime, Heartbeat: leaseHeartbeat,
		NewOwner: func() string { return uistream.RandomID("turn") },
	})
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	// Other processes' Stop requests, for as long as the server lives. A
	// broken subscription is retried; in the meantime a stop still arrives
	// with the turn's next heartbeat.
	go func() {
		for deps.Lifetime.Err() == nil {
			if err := registry.Listen(deps.Lifetime); err != nil {
				deps.Logger.WarnContext(deps.Lifetime, "the chat's stop subscription broke; retrying", "error", err.Error())
			}
			select {
			case <-deps.Lifetime.Done():
			case <-time.After(time.Second):
			}
		}
	}()

	service := &Service{
		Rows: rows, Notebooks: rows, Queue: deps.Queue, Turns: registry, Assembler: assembly, Store: checkpoints, Models: models, Choices: choices,
		Titler:  ModelTitler{},
		Timing:  Timing{MaxTurn: settings.MaxTurn, KeepAlive: keepAliveEvery, SaveEvery: saveEvery, Title: titleTimeout, Stall: stallTimeout},
		Summary: SummaryLimits{Above: summariseAbove, Keep: summaryKeep, ResultChars: summaryResultChars},
		Logger:  deps.Logger, Now: time.Now, Lifetime: deps.Lifetime,
	}
	if err := service.Check(); err != nil {
		closeAll()
		return nil, nil, err
	}
	service.Parts = Parts{
		Models: models, Documents: searcher, Academic: sources, Pages: pages,
		Web: assembly.Web, Deliverables: specialist, Checkpoints: checkpoints,
	}
	// The thread routes share the turn registry and the checkpoint store:
	// deleting a thread stops its turn, holds its lease, and removes the
	// loop's conversation of it (and every specialist's under it) before
	// the rows go.
	service.Threads = &threads.Service{
		Store: threads.Store{Pool: deps.Pool},
		Turns: registry,
		Forget: func(ctx context.Context, threadID int64) error {
			// The conversation first: it is what must not outlive the
			// thread. The sandbox would expire by itself; removing it now
			// frees its place in the account's limit.
			if err := checkpoints.DeleteTree(ctx, agentThread(threadID)); err != nil {
				return err
			}
			return forgetSandbox(ctx, threadID)
		},
		Tools:       pickerTools(),
		FrontendURL: settings.FrontendURL,
		Events:      deps.Events,
		StopWait:    threadStopWait,
		Logger:      deps.Logger,
		Now:         time.Now,
	}
	if err := service.Threads.Check(); err != nil {
		closeAll()
		return nil, nil, err
	}
	return service, closeAll, nil
}

// notChat is the proxy names this deployment's settings give a job other
// than answering: they are never offered in the model menu, whatever the
// proxy says they are for. The vision model is not among them: a model that
// reads pictures is a chat model too.
func notChat(settings Settings) []string {
	names := []string{settings.EmbeddingModel}
	if settings.ImageModel != "" {
		names = append(names, settings.ImageModel)
	}
	return names
}
