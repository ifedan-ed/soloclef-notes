package assembly

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/academic"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/accountfiles"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/accountvision"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/documents"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/drivetools"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/imapmail"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/mailsignin"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/mailtools"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/mcptools"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/nextcloudtools"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/pagereader"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/websearch"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/drivegrant"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/nextcloud"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/fileread"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/vision"
)

// The choices below are the assistant's own, not a deployment's: they are
// how its tools are meant to run, they are named, and each says why it is
// what it is. They are here, once, so that a turn answered by the API and a
// turn run by the worker search, read and connect in the same way.
const (
	// The search's sizes: how many candidates each leg contributes, the
	// most documents one search returns, and how much text, in all and per
	// document. They are the sizes the indexed corpus was tuned with.
	searchCandidates    = 200
	searchMaxDocuments  = 20
	searchContextChars  = 26000
	searchDocumentChars = 16000

	// The page reader: five pages for one approval, two megabytes a page
	// (a healthy article is well under one; a page far over it is an
	// application, not a document), twenty seconds a page, and sixty
	// thousand characters back to the model for the whole call, about
	// fifteen thousand tokens.
	pageReaderPages     = 5
	pageReaderBytes     = 2 << 20
	pageReaderTimeout   = 20 * time.Second
	pageReaderChars     = 60_000
	pageReaderRedirects = 5
	// A PDF's body is given two minutes, from the moment it is known to be
	// one: an import keeps a file up to the upload limit, which at
	// ordinary speeds arrives well inside that. The chat reads one of at
	// most fileread.MaxFileBytes (pagereader.Settings.MaxPDFBytes).
	pageReaderPDFTimeout = 2 * time.Minute

	// Connected apps (MCP). A server has thirty seconds to list its tools
	// and a call sixty, the times the account's servers have always been
	// given. At most 200 tools are taken from one server. Definitions are
	// kept five minutes at most, which bounds the one gap the shared
	// counter leaves (a writer that committed and died before counting).
	connectedAppConnect     = 10 * time.Second
	connectedAppDiscovery   = 30 * time.Second
	connectedAppCall        = 60 * time.Second
	connectedAppTools       = 200
	connectedAppLifetime    = 5 * time.Minute
	connectedAppRetry       = 30 * time.Second
	connectedAppAccounts    = 2000
	connectedAppResultBytes = 64 << 10

	// Mail. One conversation with a mail server (sign in, search or read,
	// sign out) has twenty seconds, the time a mailbox's test has always
	// been given; a search of every mailbox waits forty-five for the
	// slowest of them, so one dead server costs a line in the result and
	// not the turn.
	mailConversation = 20 * time.Second
	mailSearch       = 45 * time.Second
	// A conversation that fetches an attachment whole has a minute: an
	// attachment at the reader's 25 MB limit is about 34 MB on the wire.
	mailAttachment = 60 * time.Second

	// Google Drive. One tool call's work at Drive has thirty seconds (an
	// export of a long document is the slow case), and renewing a grant
	// ten.
	driveCall         = 30 * time.Second
	driveTokenTimeout = 10 * time.Second

	// Nextcloud. One listing or write has thirty seconds, the import's own
	// bound on a PROPFIND; a download may be silent for thirty seconds,
	// however long the whole takes; and a "slow down" answer is waited on
	// five seconds at most, since a person is waiting on the call.
	nextcloudListing      = 30 * time.Second
	nextcloudStall        = 30 * time.Second
	nextcloudMaxRetryWait = 5 * time.Second

	// Reading a file a tool fetched (fileread): one description of a
	// picture has a minute, the time the reader gives one parse.
	fileVisionTimeout = 60 * time.Second
)

// ConnectedAppConnectTimeout is how long one connection to a connected
// app's server may take to open.
const ConnectedAppConnectTimeout = connectedAppConnect

// connectedAppSiteHosts is where a site-wide connected app may be reached
// while the administrators have saved no list of their own. It is the list
// the Python tier holds for the same case (mcp_security/allowlist.py), and
// the two must stay the same: a server one tier connects to and the other
// refuses is two answers to one question.
var connectedAppSiteHosts = []string{"mcp.exa.ai", "mcp.tavily.com", "mcp.browserbase.com"}

// ConnectedAppSiteHosts is a copy of that list.
func ConnectedAppSiteHosts() []string { return slices.Clone(connectedAppSiteHosts) }

// ToolSettings is what a deployment says about the tools. Every field
// without "optional" in its comment is required; there is no default for
// anything that names a server or a model.
type ToolSettings struct {
	// ModelURL and ModelKey reach the model proxy, which embeds a search's
	// query with EmbeddingModel into EmbeddingDimension numbers: the model
	// and length the documents were indexed with.
	ModelURL, ModelKey string
	EmbeddingModel     string
	EmbeddingDimension int

	// The vector store the indexer writes to. QdrantAPIKey is optional.
	QdrantURL, QdrantAPIKey, QdrantCollection string
	QdrantHNSWEf                              int
	// QdrantQuantized says the collection stores quantized vectors; then
	// QdrantRescore and QdrantOversampling say how a search uses them.
	QdrantQuantized    bool
	QdrantRescore      bool
	QdrantOversampling float64

	// NCBITool names this deployment to PubMed; the other two are optional.
	NCBITool, NCBIEmail, NCBIAPIKey string

	// ValkeyURL is where a change to an account's connected apps is
	// counted, so every process drops its copy of that account's tools.
	ValkeyURL string
	// Brand names the product to the sites and servers the tools call.
	Brand string
	// ReservedNames are tool names the caller offers besides these, which a
	// connected app's tool must not take. Optional.
	ReservedNames []string

	// SecretKey is the deployment's secret key, which the credentials of an
	// account's mailboxes and of its Drive grant are sealed with.
	SecretKey string
	// MicrosoftMail is the app registration a Microsoft mailbox's token is
	// refreshed at. Optional as a whole: with none of it set, a Microsoft
	// mailbox answers that Microsoft mail is not set up on this server.
	// Set in part is an error.
	MicrosoftMail MicrosoftMailSettings
	// GoogleDrive is the Google project a Drive grant is renewed at.
	// Optional as a whole: with none of it set no turn is offered the
	// Drive tools. Set in part is an error.
	GoogleDrive GoogleDriveSettings
	// NextcloudURL is the deployment's one Nextcloud (NEXTCLOUD_BASE_URL),
	// the only server an account's stored app password is sent to.
	// Optional: with none, no turn is offered the Nextcloud tools.
	NextcloudURL string

	// Parsers is the deployment's parser registry (soloclef_ingest/
	// parserconfig), which an attachment or a Drive or Nextcloud file is
	// read with, by the parser its connection chose.
	Parsers fileread.Parsers
	// VisionModel is the deployment's vision model (CHAT_VISION_MODEL),
	// which describes a picture for an account that chose none. Optional:
	// with none, such an account's pictures are said not to be readable.
	VisionModel string
}

// MicrosoftMailSettings is the deployment's Microsoft app registration.
type MicrosoftMailSettings struct {
	ClientID, ClientSecret string
	// TokenURL is the tenant's token endpoint.
	TokenURL string
}

// GoogleDriveSettings is the deployment's Google project.
type GoogleDriveSettings struct {
	ClientID, ClientSecret string
}

// set reports how many of the values are set, of how many.
func set(values ...string) (count, of int) {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			count++
		}
	}
	return count, len(values)
}

// ToolDeps is what the process already has.
type ToolDeps struct {
	Pool *pgxpool.Pool
	// HTTP must have no whole-request timeout; each call is bounded by its
	// turn's context.
	HTTP   *http.Client
	Logger *slog.Logger
	// Own opens a model of an account's own connection as its vision
	// model, for a picture a file tool reads (ownmodels.Connections, the
	// one the turn's chat model is opened with).
	Own ownmodels.VisionOpener
}

// Tools are the tools of a turn that every tier builds the same way.
type Tools struct {
	Documents *documents.Searcher
	Academic  *academic.Sources
	// Pages is the page reader and PageTool the tool it is offered as,
	// which asks every time.
	Pages    *pagereader.Reader
	PageTool tool.Tool
	Web      websearch.Search
	// Connected is the accounts' connected apps.
	Connected Connections
	// Mail reads an account's connected mailboxes.
	Mail AccountTools
	// Drive works with an account's Google Drive; nil on a deployment
	// with no Google project.
	Drive AccountTools
	// Nextcloud works with an account's Nextcloud files; nil on a
	// deployment that names no Nextcloud.
	Nextcloud AccountTools
	// Files reads a file a tool fetched, for its account: the reader the
	// mail and Drive tools are given, for any other tool that fetches one.
	Files *accountfiles.Reader
}

// NewTools builds the tools. The returned function releases what was
// opened and is called once, at shutdown.
func NewTools(settings ToolSettings, deps ToolDeps) (Tools, func(), error) {
	switch {
	case deps.Pool == nil || deps.HTTP == nil || deps.Logger == nil || deps.Own == nil:
		return Tools{}, nil, errors.New("assembly: the pool, the HTTP client, the logger and the own model connections are all required")
	case strings.TrimSpace(settings.ValkeyURL) == "" || settings.Brand == "":
		return Tools{}, nil, errors.New("assembly: the Valkey address and the brand are required")
	case settings.SecretKey == "":
		return Tools{}, nil, errors.New("assembly: the secret key that stored credentials are sealed with is required")
	case settings.Parsers == nil:
		return Tools{}, nil, errors.New("assembly: the parser registry the file tools read with is required")
	}
	microsoft, google := settings.MicrosoftMail, settings.GoogleDrive
	if count, of := set(microsoft.ClientID, microsoft.ClientSecret, microsoft.TokenURL); count != 0 && count != of {
		return Tools{}, nil, errors.New("assembly: Microsoft mail's client id, client secret and token address must be set together or not at all")
	}
	if count, of := set(google.ClientID, google.ClientSecret); count != 0 && count != of {
		return Tools{}, nil, errors.New("assembly: the Google project's client id and client secret must be set together or not at all")
	}
	embedder := documents.ProxyEmbedder{
		BaseURL: settings.ModelURL, Key: settings.ModelKey, Model: settings.EmbeddingModel,
		Dimension: settings.EmbeddingDimension, HTTP: deps.HTTP,
	}
	if err := embedder.Check(); err != nil {
		return Tools{}, nil, err
	}
	index := documents.Qdrant{
		URL: settings.QdrantURL, APIKey: settings.QdrantAPIKey, Collection: settings.QdrantCollection,
		HNSWEf: settings.QdrantHNSWEf, Quantized: settings.QdrantQuantized, Rescore: settings.QdrantRescore,
		Oversampling: settings.QdrantOversampling, HTTP: deps.HTTP,
	}
	if err := index.Check(); err != nil {
		return Tools{}, nil, err
	}
	searcher, err := documents.NewSearcher(embedder, index, documents.Store{Pool: deps.Pool}, documents.Limits{
		Candidates: searchCandidates, MaxDocuments: searchMaxDocuments,
		ContextChars: searchContextChars, DocumentChars: searchDocumentChars,
	})
	if err != nil {
		return Tools{}, nil, err
	}
	sources, err := academic.New(academic.Settings{
		HTTP: deps.HTTP, NCBITool: settings.NCBITool, NCBIEmail: settings.NCBIEmail, NCBIAPIKey: settings.NCBIAPIKey,
	})
	if err != nil {
		return Tools{}, nil, err
	}

	// The reader every tool that fetches a file reads it with: the parser
	// it names, under fileread's bounds. The page reader reads a PDF's text
	// through it, so a PDF a link leads to is read as an attachment is.
	reading, err := fileread.New(settings.Parsers, deps.Logger)
	if err != nil {
		return Tools{}, nil, err
	}
	pages, err := pagereader.New(pagereader.Settings{
		MaxPages: pageReaderPages, MaxBytes: pageReaderBytes, Timeout: pageReaderTimeout,
		MaxChars: pageReaderChars, MaxRedirects: pageReaderRedirects,
		UserAgent: pageReaderAgent(settings.Brand), Allow: pagereader.PublicAddress,
		MaxPDFBytes: fileread.MaxFileBytes, PDFTimeout: pageReaderPDFTimeout, PDFs: reading,
	})
	if err != nil {
		return Tools{}, nil, err
	}
	pageTool := pages.Tool()

	generations, err := mcptools.NewValkey(settings.ValkeyURL)
	if err != nil {
		return Tools{}, nil, err
	}
	connected, err := mcptools.NewSource(mcptools.Settings{
		Rows: mcptools.Postgres{Pool: deps.Pool}, Generations: generations,
		Network: mcptools.PublicInternet{Resolve: mcptools.SystemResolve, ConnectTimeout: connectedAppConnect},
		Logger:  deps.Logger, Now: time.Now, ClientName: settings.Brand,
		SiteHostsWhenUnset: connectedAppSiteHosts,
		// Every name the chat may offer of its own, so that a connected
		// app's tool of the same name is given a name of its own instead.
		ReservedNames: append([]string{
			documents.ToolName, websearch.Exa.ToolName(), websearch.Tavily.ToolName(),
			academic.PubMedTool, academic.ArxivTool, academic.PreprintTool,
			pageTool.Name, MemoryToolName, KnowledgeBaseName,
		}, slices.Concat(mailtools.Names(), drivetools.Names(), nextcloudtools.Names(), settings.ReservedNames)...),
		Limits: mcptools.Limits{
			ToolsPerServer: connectedAppTools, Discovery: connectedAppDiscovery, Call: connectedAppCall,
			Lifetime: connectedAppLifetime, RetryIncomplete: connectedAppRetry,
			Accounts: connectedAppAccounts, ResultBytes: connectedAppResultBytes,
		},
	})
	if err != nil {
		generations.Close()
		return Tools{}, nil, err
	}

	fail := func(err error) (Tools, func(), error) {
		generations.Close()
		return Tools{}, nil, err
	}

	// The reader every tool that fetches a file reads it with: the
	// connection's parser, the account's vision model, the reader's bounds.
	// The vision model is the account's: a proxy name, or a model of its
	// own connection called at its own address (accountvision).
	proxyVision := vision.Client{BaseURL: settings.ModelURL, Key: settings.ModelKey, HTTP: deps.HTTP, Timeout: fileVisionTimeout}
	sight, err := accountvision.ForDeployment(deps.Pool, proxyVision, deps.Own, settings.VisionModel)
	if err != nil {
		return fail(err)
	}
	files := &accountfiles.Reader{Files: reading, Vision: sight}
	if err := files.Check(); err != nil {
		return fail(err)
	}

	// The account's mailboxes. A mailbox's host was typed by a person, so
	// the client connects only to public addresses (imapmail).
	mailRows := mailtools.Store{Pool: deps.Pool}
	signIn := mailsignin.SignIn{SecretKey: settings.SecretKey, Rows: mailRows, Now: time.Now, Logger: deps.Logger}
	if microsoft.ClientID != "" {
		// The process's own transport: the endpoint is Microsoft's, named
		// by the deployment, and never an address a person typed.
		signIn.Microsoft = mailsignin.NewEndpoint(microsoft.ClientID, microsoft.ClientSecret, microsoft.TokenURL, deps.HTTP.Transport)
	}
	if err := signIn.Check(); err != nil {
		return fail(err)
	}
	reader := imapmail.IMAP{Resolve: mcptools.SystemResolve, Timeout: mailConversation, AttachmentTimeout: mailAttachment}
	if err := reader.Check(); err != nil {
		return fail(err)
	}
	mail := &mailtools.Tools{Rows: mailRows, Mail: reader, SignIn: signIn, Files: files, SearchTimeout: mailSearch, Logger: deps.Logger}
	if err := mail.Check(); err != nil {
		return fail(err)
	}

	tools := Tools{
		Documents: searcher, Academic: sources, Pages: pages, PageTool: pageTool,
		Web:       websearch.Search{Keys: websearch.StoredKeys{Pool: deps.Pool}, HTTP: deps.HTTP},
		Connected: connectedApps{connected},
		Mail:      mail,
		Files:     files,
	}
	if google.ClientID != "" {
		drive := &drivetools.Tools{
			Rows: drivetools.Store{Pool: deps.Pool}, Files: files,
			Google: drivegrant.Client{ClientID: google.ClientID, ClientSecret: google.ClientSecret,
				TokenURL: drivetools.GoogleTokenURL, HTTP: drivetools.NewHTTP(driveTokenTimeout)},
			SecretKey: settings.SecretKey, DriveURL: drivetools.GoogleDriveURL, UploadURL: drivetools.GoogleUploadURL,
			HTTP: drivetools.NewHTTP(driveCall), Timeout: driveCall, Now: time.Now, Logger: deps.Logger,
		}
		if err := drive.Check(); err != nil {
			return fail(err)
		}
		tools.Drive = drive
	}
	release := generations.Close
	if strings.TrimSpace(settings.NextcloudURL) != "" {
		credentials, err := nextcloud.NewCredentials(settings.SecretKey, settings.NextcloudURL)
		if err != nil {
			return fail(err)
		}
		// A transport of its own, so its pool of connections to the one
		// Nextcloud server is reused across calls and closed at shutdown.
		transport := http.DefaultTransport.(*http.Transport).Clone()
		cloud := &nextcloudtools.Tools{
			Rows: nextcloudtools.Store{Pool: deps.Pool}, Files: files, Credentials: credentials, Transport: transport,
			Timing: nextcloud.Timing{Listing: nextcloudListing, Stall: nextcloudStall, MaxRetryWait: nextcloudMaxRetryWait},
			Logger: deps.Logger,
		}
		if err := cloud.Check(); err != nil {
			transport.CloseIdleConnections()
			return fail(err)
		}
		tools.Nextcloud = cloud
		release = func() {
			transport.CloseIdleConnections()
			generations.Close()
		}
	}
	return tools, release, nil
}

// connectedApps is the MCP package's Source as the assembly's Connections.
// The two are kept apart by this one small type so that neither package
// imports the other's vocabulary.
type connectedApps struct{ source *mcptools.Source }

func (c connectedApps) ForTurn(ctx context.Context, userID string) (ConnectedTools, error) {
	set, err := c.source.ForTurn(ctx, userID)
	if err != nil {
		return nil, err
	}
	return connectedSet{set}, nil
}

type connectedSet struct{ *mcptools.Set }

// Trust remembers "always allow" on the app's connection. A connection
// removed or switched off while the turn waited has no list left to write
// to: the approval still holds for the turn it was given in (the loop keeps
// that), so this is not treated as a failure of the approval.
func (c connectedSet) Trust(ctx context.Context, name string) error {
	if err := c.Set.Trust(ctx, name); err != nil && !errors.Is(err, mcptools.ErrGone) {
		return err
	}
	return nil
}

func (c connectedSet) App(name string) (string, bool) {
	info, mine := c.Lookup(name)
	return info.Server, mine
}

// pageReaderAgent is how the page reader names itself to the sites it
// reads: the product's name, so a site's owner can tell who came.
func pageReaderAgent(brand string) string {
	var name strings.Builder
	for _, r := range brand {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			name.WriteRune(r)
		}
	}
	return name.String() + "PageReader/1.0"
}
