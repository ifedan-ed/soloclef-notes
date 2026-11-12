// Command api is Soloclef's HTTP API: every route under /api/v1, /auth and
// /users, the chat included, in one process.
//
// The proxy in front of it (docker/proxy-go) sends it those three prefixes and
// nothing else; a path no route here matches is this process's own 404. The
// answers keep the shapes the Python version gave (the error envelope, the
// order a request is refused in, the 422 fields), because the web app and the
// phone app were written against them and still read them.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_schema/schemaversion"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/academicsearch"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/accesstokens"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/accountprofile"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/activitylogs"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/activitysummary"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/agentactions"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/agentflags"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/agentrules"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/agentsignin"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/apphandoff"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/appreturn"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/artifacts"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/automations"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/celeryqueue"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/chat"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/chat/threads"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/clienterrors"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/config"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/connectionrevocation"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/corsmirror"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/csrforigin"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/deliverablejobs"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/documentcontent"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/documentdeletion"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/documentfiles"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/documentlibrary"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/documentlifecycle"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/documents"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/documentsearch"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/driveconnect"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/drivesync"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/e2eseed"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/eventstream"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/folders"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/gatedanalytics"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/health"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/indexqueue"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/instructionslint"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/knowledgebase"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/mailaccounts"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/markdownmemory"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/medialinks"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/microsoftmail"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/modelchoices"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/modelconnections"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/nextcloudconnect"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/nextcloudimport"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/notebookcopy"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/notebookfollowups"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/notebooklinks"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/notebookresearch"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/notebooks"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/notebooksearch"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/notebooksources"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/notes"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/notifications"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/oidcsignin"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/podcasts"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/posthogcapture"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/prompts"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/publicchat"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/publiclinks"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/publicshare"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pushdevices"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/ratelimit"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/routeanalytics"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/searchconnectors"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/signin"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/signinoptions"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/siteannouncements"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/sourceguides"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/starlettepath"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/studiogenerate"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/studiooutputs"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/studioviews"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/supportcontact"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/uploadvision"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/deliverables"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/mcptools"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/pagereader"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/voices"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/nextcloud"
	filestore "git.danvics.com/danvics/soloclef/soloclef_filestore"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parserconfig"
	"git.danvics.com/danvics/soloclef/soloclef_studio/sandbox"
)

func main() {
	// `soloclef-api healthcheck` asks the server in this container whether it
	// is serving and says so with its exit status (healthcheck.go). It is
	// decided here, before run, because it must not open a pool, a queue or a
	// listener of its own.
	//
	// `soloclef-api set-password <address>` makes an account or gives one a
	// new password, read from standard input (set_password.go).
	if len(os.Args) == 3 && os.Args[1] == setPasswordCommand {
		if err := setPassword(os.Args[2], os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	// `soloclef-api set-admin <address> true|false` says whether an account
	// administers the deployment (set_admin.go).
	if len(os.Args) == 4 && os.Args[1] == setAdminCommand {
		if err := setAdmin(os.Args[2], os.Args[3], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 {
		if len(os.Args) != 2 || os.Args[1] != healthcheckCommand {
			fmt.Fprintf(os.Stderr, "usage: %s [%s | %s <address> | %s <address> true|false]\n",
				os.Args[0], healthcheckCommand, setPasswordCommand, setAdminCommand)
			os.Exit(2)
		}
		if err := healthcheck(os.Getenv); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		// Nothing is configured yet at this point, so this is the one place
		// that writes a bare line.
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	settings, err := config.FromEnvironment()
	if err != nil {
		return err
	}
	logger := newLogger(settings.LogLevel)

	// A signalled shutdown stops accepting and lets in-flight reads finish.
	ctx, stopListeningForSignals := signal.NotifyContext(
		context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopListeningForSignals()

	startup, cancelStartup := context.WithTimeout(ctx, 30*time.Second)
	defer cancelStartup()
	pool, err := database.Open(startup, settings.DatabaseURL, settings.DatabaseMaxConnections)
	if err != nil {
		return err
	}
	defer pool.Close()
	// Before anything is built on the pool: a database that is not at the
	// schema version this binary was built against stops the start here,
	// with the version it is at and the command that fixes it. This process
	// never migrates; `migrate up` is a deployment's own step
	// (soloclef_schema/schemaversion says why).
	if err := schemaversion.Check(startup, pool); err != nil {
		return err
	}

	errors := errorenvelope.Writer{ReportURL: settings.ErrorReportURL}

	// One queue for every product event this tier sends: the gated-request
	// middleware's and each write's own (internal/posthogcapture). It is built
	// before the routes because a write holds a capture of its own, and not at
	// all when this deployment has no project key.
	var events *posthogcapture.Queue
	if settings.PostHogAPIKey != "" {
		events = posthogcapture.NewQueue(settings.PostHogAPIKey, settings.PostHogHost, logger)
		defer events.Close()
	}
	var writeAnalytics routeanalytics.Capture
	if events != nil {
		writeAnalytics = routeanalytics.Capture{Sink: events}
	}

	// The queue every write's background work goes on. It connects on first
	// use, as Celery's own client does, so a broker that is down does not stop
	// this process from serving the reads it owns (internal/celeryqueue).
	queue, err := celeryqueue.New(settings.CeleryBrokerURL, settings.CeleryDefaultQueue)
	if err != nil {
		return err
	}
	defer queue.Close()

	// The event bus, which the two document moves publish a folder arrival on.
	// It is the same Redis the limiter counts in, as it is there, and it
	// connects on first use for the same reason the queue does.
	arrivals, err := eventstream.NewPublisher(settings.RedisURL)
	if err != nil {
		return err
	}
	defer arrivals.Close()

	api := humago.New(http.NewServeMux(), huma.DefaultConfig("Soloclef API", "0.1.0"))
	activitysummary.Route{
		Logs:   activitysummary.Reader{Pool: pool},
		Errors: errors,
		Logger: logger,
	}.Register(api)
	accesstokens.Routes{
		Pool:      pool,
		Tokens:    accesstokens.Store{Pool: pool},
		Analytics: writeAnalytics,
		Errors:    errors,
		Logger:    logger,
	}.Register(api)
	// Two of the thirteen artifact routes answer from rows alone and are
	// registered here. The delete and the ten that read an artifact's bytes
	// need the file stores, and are registered once those are open, below.
	artifactRows := artifacts.Store{Pool: pool}
	artifacts.ListRoute{
		Artifacts: artifactRows,
		Errors:    errors,
		Logger:    logger,
	}.Register(api)
	artifacts.VideoRoute{
		Videos: artifactRows,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	// The other ten read an artifact's bytes out of the file store: the
	// manifest and the six study writes read a deck or a quiz (remembered
	// once read, StudyFiles), and three stream a file with Range and 206.
	//
	// Each row is read from the backend it recorded, so every backend the
	// deployment gave settings for is opened here, not only the configured
	// one (filestore.Stores).
	fileStores, err := filestore.OpenStores(fileStorageSettings(settings))
	if err != nil {
		return err
	}
	defer fileStores.Close()

	// The removal of a document and everything stored for it: its points in
	// the vector store, its chunk rows, its stored files, its row, in the
	// order that makes a failure at any step harmless and a retry complete
	// it (internal/documentdeletion). It is built once, here, after the file
	// stores are open, and every route that deletes a document is given this
	// one: the document delete, the note delete, the artifact delete and the
	// removal of a connection with its documents. Nothing in this tier hands
	// one document's deletion to a queue.
	//
	// The points are in the collection the chat searches, so the address,
	// the key and the name are the chat's own settings: one collection, said
	// once. The client has no Timeout because each removal runs under its
	// own deadline.
	documentVectors := documentdeletion.Qdrant{
		URL:        settings.QdrantURL,
		APIKey:     settings.QdrantAPIKey,
		Collection: settings.QdrantChunkCollection,
		HTTP:       &http.Client{},
	}
	if err := documentVectors.Check(); err != nil {
		return err
	}
	removal := documentdeletion.Removal{
		Rows: documentdeletion.PostgresRows{Pool: pool}, Vectors: documentVectors, Files: fileStores,
	}

	// The parsers, read from the same settings the indexer reads
	// (soloclef_ingest/parserconfig: SOLOCLEF_PARSER_DEFAULT and each
	// parser's own, none of them prefixed for this tier). They have to be
	// the same in both processes: this tier accepts a parser for a file and
	// writes its name on the row, and the indexer is the one that runs it.
	// A parser configured here and not there would accept uploads that then
	// fail; Load refuses a half-configured parser in either.
	parsers, err := parserconfig.Load(os.Getenv)
	if err != nil {
		return err
	}
	uploadLimits := parserchoice.Limits{MaxFileBytes: settings.UploadMaxFileBytes, MaxFiles: settings.UploadMaxFiles}

	// A document's life: the capabilities the pickers read, the upload and
	// the folder upload, the re-index, the delete, and a connection's
	// parser. They are mounted from one place because they must share one
	// registry, one set of limits, one queue and one set of stores
	// (internal/documentlifecycle). The two uploads replace the server's
	// read timeout with a limit on *stalling*, which is the write timeout's
	// figure for the reason the streams below use it.
	// Whether an account may ask for its uploads' pictures to be described:
	// it, or the deployment, has a vision model. The deployment's is the
	// chat's own, the one the indexer is given too
	// (SOLOCLEF_INDEX_VISION_MODEL). One rule, for the uploads, the
	// Nextcloud import and the capabilities the dialogs read.
	uploadVision := uploadvision.Vision{Choices: modelchoices.Rows{Pool: pool}, Default: settings.ChatVisionModel}
	if err := documentlifecycle.Register(api, documentlifecycle.Dependencies{
		Pool:        pool,
		Queue:       queue,
		Files:       fileStores,
		Parsers:     parsers,
		Vectors:     documentVectors,
		Vision:      uploadVision,
		Limits:      uploadLimits,
		UploadStall: writeTimeout,
		Errors:      errors,
		Logger:      logger,
	}); err != nil {
		return err
	}

	// An artifact is deleted by removing the document it stands on, before
	// the route answers. It used to queue the Python tier's delete task.
	artifacts.DeleteRoute{
		Deletions: artifactRows,
		Removal:   removal,
		Errors:    errors,
		Logger:    logger,
	}.Register(api)

	artifactGate := artifacts.Gate{Errors: errors, Logger: logger}
	studyFiles := artifacts.NewStudyFiles(fileStores)
	artifacts.ManifestRoute{Gate: artifactGate, Manifests: artifactRows, StudyFiles: studyFiles}.Register(api)
	// The three that send bytes hold a client to the server's write timeout
	// as a limit on *stalling*, not on the whole response (writeTimeout).
	artifacts.DownloadRoute{Gate: artifactGate, Downloads: artifactRows, Files: fileStores,
		StallTimeout: writeTimeout}.Register(api)
	artifacts.FileContentRoute{Gate: artifactGate, Rows: artifactRows, Files: fileStores,
		StallTimeout: writeTimeout}.Register(api)
	artifacts.SlideAudioRoute{Gate: artifactGate, Slides: artifactRows, Files: fileStores,
		StallTimeout: writeTimeout}.Register(api)
	artifacts.FlashcardRoutes{Gate: artifactGate, Study: artifactRows, StudyFiles: studyFiles}.Register(api)
	artifacts.QuizRoutes{Gate: artifactGate, Study: artifactRows, StudyFiles: studyFiles}.Register(api)
	logStore := activitylogs.Store{Pool: pool}
	activitylogs.ReadRoute{
		Logs:   logStore,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	activitylogs.CreateRoute{
		Logs:   logStore,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	activitylogs.UpdateRoute{
		Logs:   logStore,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	activitylogs.DeleteRoute{
		Logs:   logStore,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	// The task log list: a page of the caller's own entries, filtered.
	activitylogs.ListRoute{Logs: logStore, Errors: errors, Logger: logger}.Register(api)
	memory := markdownmemory.Store{Pool: pool}
	markdownmemory.UserRoutes{
		Pool:   pool,
		Memory: memory,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	folderStore := folders.Store{Pool: pool}
	folders.ReadRoutes{
		Folders: folderStore,
		Errors:  errors,
		Logger:  logger,
	}.Register(api)
	folders.ListRoute{
		Folders: folderStore,
		Errors:  errors,
		Logger:  logger,
	}.Register(api)
	folders.CreateRoute{
		Folders: folderStore,
		Errors:  errors,
		Logger:  logger,
	}.Register(api)
	folders.RenameRoute{
		Folders: folderStore,
		Errors:  errors,
		Logger:  logger,
	}.Register(api)
	folders.MoveRoute{
		Folders: folderStore,
		Errors:  errors,
		Logger:  logger,
	}.Register(api)
	folders.ReorderRoute{
		Folders: folderStore,
		Errors:  errors,
		Logger:  logger,
	}.Register(api)
	folders.WatchRoute{
		Folders: folderStore,
		Errors:  errors,
		Logger:  logger,
	}.Register(api)
	folders.DeleteRoute{
		Folders: folderStore,
		Queue:   queue,
		Errors:  errors,
		Logger:  logger,
	}.Register(api)
	promptStore := prompts.Store{Pool: pool}
	// Who may publish a prompt to every account is the account's own
	// is_superuser flag, read by the two writes themselves
	// (internal/prompts/admin.go); there is no setting to hand them.
	prompts.ReadRoutes{
		Prompts: promptStore,
		Errors:  errors,
		Logger:  logger,
	}.Register(api)
	prompts.CreateRoute{
		Pool:   pool,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	prompts.UpdateRoute{
		Pool:   pool,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	prompts.DeleteRoute{
		Pool:   pool,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	prompts.CopyRoute{
		Pool:   pool,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	folders.DocumentMoveRoute{
		Folders:  folderStore,
		Arrivals: folderArrivals{events: arrivals},
		Errors:   errors,
		Logger:   logger,
	}.Register(api)
	folders.BulkMoveRoute{
		Folders:  folderStore,
		Arrivals: folderArrivals{events: arrivals},
		Errors:   errors,
		Logger:   logger,
	}.Register(api)
	// The caller's public share links: the list and the revoke
	// (internal/publiclinks). Both are the caller's own links and no others.
	publicLinks := publiclinks.Store{Pool: pool}
	publiclinks.ListRoute{
		Links:       publicLinks,
		FrontendURL: settings.FrontendURL,
		Errors:      errors,
		Logger:      logger,
	}.Register(api)
	publiclinks.RevokeRoute{
		Links:  publicLinks,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	// What a snapshot link serves besides its page: the files of the
	// artifacts it lists, a shared podcast's audio, a shared video; and the
	// two things a signed-in person does with one, making an output's link
	// and copying a shared chat into their own account
	// (internal/publicshare). The streams are paced like every other.
	publicShare := publicshare.Routes{
		Pool: pool, Files: fileStores, StallTimeout: writeTimeout,
		FrontendURL: settings.FrontendURL, Analytics: writeAnalytics,
		Errors: errors, Logger: logger,
	}
	if err := publicShare.Check(); err != nil {
		return err
	}
	publicShare.Register(api)
	// A notebook's live link: the owner's five routes and the six a
	// visitor's page reads by token (internal/notebooklinks).
	notebookLinks := notebooklinks.Routes{
		Pool: pool, Files: fileStores, StallTimeout: writeTimeout,
		FrontendURL: settings.FrontendURL, Errors: errors, Logger: logger,
	}
	if err := notebookLinks.Check(); err != nil {
		return err
	}
	notebookLinks.Register(api)
	// Duplicating a notebook within the caller's own account.
	notebookcopy.Route{Pool: pool, Errors: errors, Logger: logger}.Register(api)

	// The account slices, each registered as a whole statement so a merge
	// boundary cannot fall inside a struct literal
	// (custom/docs/go-api-tier.md).
	profileStore := accountprofile.Store{Pool: pool}
	accountprofile.ReadRoute{
		Profiles: profileStore,
		Errors:   errors,
		Logger:   logger,
	}.Register(api)
	accountprofile.UpdateRoute{
		Pool:   pool,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	// The account's own settings, API access among them: the switch the gate
	// reads for every key and agent (principal.ErrAPIAccessOff).
	accountprofile.SettingsRoutes{
		Settings: profileStore,
		Errors:   errors,
		Logger:   logger,
	}.Register(api)
	accountprofile.DeleteRoute{
		Pool:  pool,
		Queue: queue,
		Cookies: accountprofile.CookiePolicy{
			SessionName:  settings.SessionCookieName,
			RefreshName:  settings.RefreshCookieName,
			Domain:       settings.CookieDomain,
			SameSite:     settings.SessionCookieSameSite,
			SecurePolicy: settings.SessionCookieSecurePolicy,
		},
		Errors: errors,
		Logger: logger,
	}.Register(api)

	// Signing in and staying signed in: the web app's and the phone app's
	// password sign-ins, the refresh, the sign-out and the session read,
	// with the sign-up form, the password reset by mail and the credential
	// check (internal/signin). All but the session read and the credential
	// check take no credential and are mounted outside the gate
	// (publicRoutes); the access tokens they mint are the ones the gate
	// verifies, with the same secret.
	mail, err := resetMailer(settings, logger)
	if err != nil {
		return err
	}
	// The end-to-end test routes (internal/e2eseed), in a stack that
	// switched them on and never otherwise: this block is the only place
	// they are mounted. Such a stack's reset mail is kept in memory for the
	// suite to read, so the outbox takes the mailer's place (the settings
	// refuse a stack that asks for both). The routes are handed to the
	// front door on their own list, outside the gate, the limiter and the
	// served-route inventory: they are not routes of the product.
	var testOnlyRoutes []string
	if settings.E2ERoutes {
		seedFiles, err := fileStores.Writer()
		if err != nil {
			return err
		}
		seedOutbox := &e2eseed.Outbox{}
		mail = seedOutbox
		seeds := e2eseed.Routes{
			Pool: pool, Files: seedFiles, Secret: settings.E2ESeedSecret, Outbox: seedOutbox,
			MintSession: func(ctx context.Context, userID string, now time.Time) (string, error) {
				return signin.MintSession(ctx, pool, settings.SessionSecret, userID, now)
			},
			Errors: errors, Logger: logger,
		}
		if err := seeds.Check(); err != nil {
			return err
		}
		seeds.Register(api)
		testOnlyRoutes = e2eseed.Public()
	}
	sessions := signin.Routes{
		Pool:   pool,
		Secret: settings.SessionSecret,
		Cookies: signin.CookiePolicy{
			SessionName:  settings.SessionCookieName,
			RefreshName:  settings.RefreshCookieName,
			Domain:       settings.CookieDomain,
			SameSite:     settings.SessionCookieSameSite,
			SecurePolicy: settings.SessionCookieSecurePolicy,
		},
		// Four Argon2id checks at once, 64 MiB each: a burst of sign-ins
		// waits rather than taking the container's memory.
		Hasher: signin.NewHasher(4),
		Errors: errors,
		Logger: logger,
		// One switch for both ways a person makes their own account: the
		// sign-up form here and a first single sign-on below.
		SignupAllowed: settings.SignupAllowed,
		Mail:          mail,
		FrontendURL:   settings.FrontendURL,
		BrandName:     settings.BrandName,
	}
	sessions.Register(api)
	// Single sign-on through the deployment's OpenID Connect provider
	// (internal/oidcsignin), ending in the same session as a password.
	sso, err := singleSignOn(settings, pool, sessions, errors, logger)
	if err != nil {
		return err
	}
	sso.Register(api)
	// What the sign-in screens may offer, for anyone: the phone app reads
	// it before anyone has signed in (internal/signinoptions).
	signinoptions.Route{Settings: signinoptions.Settings{
		SMTPEnabled: settings.SMTPEnabled, SignupAllowed: settings.SignupAllowed, OIDCEnabled: settings.OIDCEnabled,
		ButtonLabel: settings.OIDCButtonLabel, DisplayName: settings.OIDCDisplayName, AppRedirects: settings.OIDCAppRedirects,
	}}.Register(api)
	// The phone's push registration and its choices (internal/pushdevices).
	// There is no push sender in Go yet: these keep the rows a sender reads.
	pushdevices.Routes{Database: pool, Errors: errors, Logger: logger}.Register(api)

	// Notes: ten routes (internal/notes). A note is a document, so a save
	// queues its indexing on the queue an upload's goes through (delayed, so
	// autosave is one run per burst of typing) and a delete is the document
	// removal: vectors, chunks, stored files and row.
	notes.Routes{
		Pool:     pool,
		Indexing: indexqueue.Indexing{Queue: queue, Marker: indexqueue.PostgresMarker{Pool: pool}, Logger: logger},
		Removal:  removal,
		Errors:   errors,
		Logger:   logger,
		// The deployment's own choice (SOLOCLEF_API_NOTE_INDEX_DELAY_SECONDS).
		IndexDelay: settings.NoteIndexDelay,
	}.Register(api)

	// The account's agent permission rules: four routes over one store,
	// each confined to the caller's own rules (internal/agentrules).
	agentrules.Routes{
		Store: agentrules.Store{Pool: pool},
		Available: agentrules.Availability{
			Disabled:          settings.DisableNewAgentStack,
			PermissionEnabled: settings.PermissionEnabled,
		},
		Errors: errors,
		Logger: logger,
	}.Register(api)

	// The documents slice: nine routes that answer from the caller's own
	// rows -- a document's chunks, its versions, one version and the
	// restore, and four reads the document browser makes about all of the
	// caller's documents (status, counts by type, watched folders, the title
	// search).
	//
	// The citation route is `/api/v1/chunks/{chunk_id}/document`: the
	// Python tier's `/documents/by-chunk/{chunk_id}` overlaps the chunk page
	// on a ServeMux and could not be mounted (internal/documents).
	documentRows := documents.Store{Pool: pool}
	documents.ByChunkRoute{
		Chunks: documentRows,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	documents.ChunksRoute{
		Chunks: documentRows,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	documents.VersionsRoute{
		Versions: documentRows,
		Errors:   errors,
		Logger:   logger,
	}.Register(api)
	documents.VersionRoute{
		Versions: documentRows,
		Errors:   errors,
		Logger:   logger,
	}.Register(api)
	// The one write of the slice. It enqueues the reindex on the queue the
	// document delete uses, and its clock is the application's because the
	// version is stamped and aged with the process's clock, not the
	// database's.
	documents.RestoreRoute{
		Restores: documentRows,
		Queue:    queue,
		Errors:   errors,
		Logger:   logger,
		Now:      time.Now,
	}.Register(api)
	documents.StatusRoute{
		Statuses: documentRows,
		Errors:   errors,
		Logger:   logger,
	}.Register(api)
	documents.TypeCountsRoute{
		Counts: documentRows,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	documents.WatchedFoldersRoute{
		Folders: documentRows,
		Errors:  errors,
		Logger:  logger,
	}.Register(api)
	documents.TitlesRoute{
		Titles: documentRows,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	// A document's text as the viewer and the note editor open it: one
	// statement naming the owner, and nothing written by the read
	// (internal/documentcontent).
	documentcontent.Route{Database: pool, Errors: errors, Logger: logger}.Register(api)
	// The document library: the list, the title-and-text search, one
	// document, adding a written one and replacing a document's text
	// (internal/documentlibrary). A new or saved document is indexed
	// through the queue and marker an upload's goes through.
	documentlibrary.Routes{
		Pool:     pool,
		Indexing: indexqueue.Indexing{Queue: queue, Marker: indexqueue.PostgresMarker{Pool: pool}, Logger: logger},
		Errors:   errors,
		Logger:   logger,
	}.Register(api)
	// A document's stored files: the viewer's manifest, the original for
	// download and a file's bytes for the viewer, each with Range and
	// paced by the write timeout as a limit on stalling, like the artifact
	// streams (internal/documentfiles).
	documentfiles.Routes{Pool: pool, Files: fileStores, StallTimeout: writeTimeout, Errors: errors, Logger: logger}.Register(api)

	// What a public share link shows to whoever holds it: three of the ten
	// routes of app/routes/public_chat_routes.py. None takes a caller -- the
	// share token in the path is the whole of the authorisation -- so they
	// are registered here as any route is and made public by where frontDoor mounts their paths: outside the
	// gate (publicRoutes, below). Access is absent for the same reason.
	//
	// Now is left nil, which is the system clock a link's expiry is measured
	// against; only a test names another.
	sharedRows := publicchat.Store{Pool: pool}
	publicchat.PageRoute{
		Rows:   sharedRows,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	publicchat.VideoRoute{
		Rows:   sharedRows,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	publicchat.PodcastRoute{
		Rows:   sharedRows,
		Errors: errors,
		Logger: logger,
	}.Register(api)

	// The providers a connection can be made to, which is a constant and so
	// takes no store, and the caller's own connections with their models
	// (internal/modelconnections, which is also where it says that a stored
	// key is never read by this process, let alone answered).
	modelconnections.ProvidersRoute{
		Errors: errors,
		Logger: logger,
	}.Register(api)
	modelconnections.ConnectionsRoute{
		Connections: modelconnections.Store{Pool: pool},
		Errors:      errors,
		Logger:      logger,
	}.Register(api)
	// Making, changing, removing and testing a connection, in a session:
	// anyone's for an OpenAI-compatible endpoint, an administrator's for
	// every other provider. The key and the headers are sealed under the
	// deployment's secret before they are stored. An OpenAI-compatible
	// endpoint is listed and tested at its own address through the egress
	// guard; every other provider through the model proxy.
	connectionKeys, err := modelconnections.NewKeySeal(settings.SessionSecret)
	if err != nil {
		return err
	}
	connectionProber := modelconnections.ProxyProber{
		URL: settings.LiteLLMURL, Key: settings.LiteLLMKey,
		HTTP: &http.Client{}, Timeout: modelTestTimeout,
	}
	if err := connectionProber.Check(); err != nil {
		return err
	}
	// Discovery reads the proxy's own catalogue of each provider's models,
	// kept for an hour; no provider is asked (internal/modelconnections).
	modelCatalogue := &modelconnections.ProxyCatalogue{
		URL: settings.LiteLLMURL, Key: settings.LiteLLMKey,
		HTTP: &http.Client{}, Timeout: modelTestTimeout, Fresh: modelCatalogueFresh,
	}
	if err := modelCatalogue.Check(); err != nil {
		return err
	}
	ownRows := ownmodels.Store{Pool: pool}
	connectionEndpoints := modelconnections.EndpointClient{
		Timeout: modelTestTimeout,
		Egress: ownmodels.Egress{
			Resolve: pagereader.SystemResolve, AllowedHosts: ownRows.AllowedHosts, ConnectTimeout: modelTestTimeout,
		},
	}
	if err := connectionEndpoints.Check(); err != nil {
		return err
	}
	connectionWrites := modelconnections.WriteRoutes{
		Connections: modelconnections.Store{Pool: pool},
		Keys:        connectionKeys,
		Prober:      connectionProber,
		Catalogue:   modelCatalogue,
		Endpoints:   connectionEndpoints,
		Errors:      errors,
		Logger:      logger,
	}
	if err := connectionWrites.Check(); err != nil {
		return err
	}
	connectionWrites.Register(api)
	// The operator's connections, as every account is shown them: the
	// provider and its models, never an address or a key.
	modelconnections.GlobalRoutes{Connections: modelconnections.Store{Pool: pool}, Errors: errors, Logger: logger}.Register(api)

	// The account's own connections and MCP servers: the two lists, one
	// connection and one MCP server. Every statement behind them names the
	// caller as the owner (internal/searchconnectors/queries.sql).
	//
	// The writes are registered further down, once the counter they move
	// exists: each changes which MCP tools the account's chat may be offered,
	// and the chat's cache of them is invalidated across processes by a
	// counter in Valkey (soloclef_assistant/mcptools; searchconnectors.ToolChanges).
	connectorRows := searchconnectors.Store{Pool: pool}
	searchconnectors.ListRoute{
		Rows:   connectorRows,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	searchconnectors.ServersRoute{
		Rows:   connectorRows,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	searchconnectors.OneRoute{
		Rows:   connectorRows,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	searchconnectors.ServerRoute{
		Rows:   connectorRows,
		Errors: errors,
		Logger: logger,
	}.Register(api)

	// The counter the chat's MCP tool cache reads: the same key in the same
	// Valkey, and the chat package's own type, so that the writer here and
	// the reader there cannot spell it differently. It connects on first
	// use.
	toolChanges, err := mcptools.NewValkey(settings.RedisURL)
	if err != nil {
		return err
	}
	defer toolChanges.Close()

	// Nextcloud, when this deployment names a server. The credentials reader
	// is what limits a stored app password to that one server; it is shared
	// by the import routes further down and by the revocation here.
	var nextcloudCredentials *nextcloud.Credentials
	if settings.NextcloudBaseURL != "" {
		// The session secret is the Python tier's SECRET_KEY, which is also
		// the key it encrypts stored tokens with.
		credentials, err := nextcloud.NewCredentials(settings.SessionSecret, settings.NextcloudBaseURL)
		if err != nil {
			return err
		}
		nextcloudCredentials = &credentials
	}
	// Ending a removed connection's credential at its provider. One
	// transport for both providers and for the Nextcloud downloads: the
	// standard one's defaults (a connection pool, the environment's proxy),
	// cloned so that nothing else in the process shares its pool.
	providers := http.DefaultTransport.(*http.Transport).Clone()
	defer providers.CloseIdleConnections()
	revocation, err := connectionrevocation.New(settings.SessionSecret, nextcloudCredentials, providers, logger)
	if err != nil {
		return err
	}

	// Connecting and disconnecting Nextcloud with its Login Flow
	// (internal/nextcloudconnect). Mounted in every deployment: without a
	// server named, the status route says so and the sign-in routes answer
	// 503, which is what the page shows as "not set up". A started sign-in
	// waits in Valkey, where the Python tier kept it under the same key.
	nextcloudLogin := nextcloudconnect.Routes{
		Rows:   nextcloudconnect.Store{Pool: pool},
		Now:    time.Now,
		Errors: errors,
		Logger: logger,
	}
	if nextcloudCredentials != nil {
		pendingLogins, err := nextcloudconnect.NewValkey(settings.RedisURL)
		if err != nil {
			return err
		}
		defer pendingLogins.Close()
		loginAPI, err := nextcloudconnect.NewLoginAPI(nextcloudCredentials.ServerURL, providers, settings.BrandName)
		if err != nil {
			return err
		}
		nextcloudLogin.Credentials, nextcloudLogin.API = nextcloudCredentials, loginAPI
		nextcloudLogin.Pending = nextcloudconnect.Pending{
			Store: pendingLogins, SecretKey: settings.SessionSecret, Now: time.Now,
		}
	}
	if err := nextcloudLogin.Check(); err != nil {
		return err
	}
	nextcloudLogin.Register(api)

	// The account's IMAP mailboxes (internal/mailaccounts): added with an
	// app password that is stored sealed, listed, removed and tested. A
	// test connects to the host a person typed, so it resolves the name
	// itself, refuses anything but public addresses and connects to the
	// address it checked.
	//
	// A mailbox may also be connected through Microsoft's sign-in
	// (internal/microsoftmail): the consent start, Microsoft's callback,
	// and the token sign-in a test of such a mailbox makes. Mounted in
	// every deployment; one that has not set it up says so. Microsoft's
	// own addresses are fields only so a test can stand in for Microsoft.
	microsoftSettings := microsoftmail.Settings{
		ClientID:     settings.MicrosoftMailClientID,
		ClientSecret: settings.MicrosoftMailClientSecret,
		Tenant:       settings.MicrosoftMailTenant,
		RedirectURI:  settings.MicrosoftMailRedirectURI,
		FrontendURL:  settings.FrontendURL,
		SecretKey:    settings.SessionSecret,
		AuthorizeURL: microsoftmail.AuthorizeURL(settings.MicrosoftMailTenant),
		TokenURL:     microsoftmail.TokenURL(settings.MicrosoftMailTenant),
	}
	// The phone app's browser hand-off keeps its one-time secrets in
	// Valkey (internal/apphandoff). Made here because the two connects'
	// callbacks hold an app's grant in it, so they are given it before
	// they are registered.
	handoffStore, err := apphandoff.NewValkey(settings.RedisURL)
	if err != nil {
		return err
	}
	defer handoffStore.Close()
	handoffSecrets := apphandoff.Secrets{Store: handoffStore}
	microsoftRoutes, err := microsoftmail.NewRoutes(microsoftmail.Store{Pool: pool}, microsoftSettings, providers, time.Now, errors, logger)
	if err != nil {
		return err
	}
	microsoftRoutes.AppHold = handoffSecrets.Holder(appreturn.MicrosoftMail)
	microsoftRoutes.Register(api)
	microsoftSignIn, err := microsoftmail.NewSignIn(microsoftmail.Store{Pool: pool}, microsoftSettings, providers, time.Now, logger)
	if err != nil {
		return err
	}
	mailboxes := mailaccounts.Routes{
		Rows:      mailaccounts.Store{Pool: pool},
		Mail:      mailaccounts.IMAP{Resolve: mcptools.SystemResolve, Timeout: 20 * time.Second},
		Microsoft: microsoftSignIn,
		SecretKey: settings.SessionSecret,
		Now:       time.Now,
		Errors:    errors,
		Logger:    logger,
	}
	if err := mailboxes.Check(); err != nil {
		return err
	}
	mailboxes.Register(api)

	// Connecting Google Drive (internal/driveconnect): the consent start,
	// Google's callback, the Picker's token and disconnecting. Mounted in
	// every deployment; one that has not set Drive up answers which
	// settings are missing. Google's own addresses are named here and are
	// fields there only so a test can stand in for Google.
	driveSettings := driveconnect.Settings{
		ClientID:     settings.GoogleOAuthClientID,
		ClientSecret: settings.GoogleOAuthClientSecret,
		RedirectURI:  settings.GoogleDriveRedirectURI,
		PickerAPIKey: settings.GooglePickerAPIKey,
		PickerAppID:  settings.GooglePickerAppID,
		FrontendURL:  settings.FrontendURL,
		SecretKey:    settings.SessionSecret,
		AuthURL:      driveconnect.GoogleAuthURL,
		TokenURL:     driveconnect.GoogleTokenURL,
		RevokeURL:    driveconnect.GoogleRevokeURL,
		AboutURL:     driveconnect.GoogleAboutURL,
	}
	driveRoutes, err := driveconnect.NewRoutes(driveconnect.Store{Pool: pool}, driveSettings, providers, time.Now, errors, logger)
	if err != nil {
		return err
	}
	driveRoutes.AppHold = handoffSecrets.Holder(appreturn.DriveConnect)
	driveRoutes.Register(api)
	// Importing picked Drive files, and "sync now" (internal/drivesync).
	// Neither talks to Google: they write the documents and publish the
	// worker's job. Mounted in every deployment, like the connect; one
	// with no Google project answers 503 and writes nothing.
	driveSync := drivesync.Routes{
		Rows:       drivesync.Store{Pool: pool},
		Parsers:    parsers,
		Queue:      queue,
		Configured: len(driveSettings.Missing()) == 0,
		Errors:     errors,
		Logger:     logger,
	}
	if err := driveSync.Check(); err != nil {
		return err
	}
	driveSync.Register(api)
	// The phone app's browser hand-off (internal/apphandoff): the Drive
	// Picker's page and the two connects, each over the same packages the
	// website's own flows use.
	handoff := apphandoff.Routes{
		Secrets:   handoffSecrets,
		Drive:     driveRoutes,
		Microsoft: microsoftRoutes,
		Imports:   driveSync,
		Notebooks: drivesync.Store{Pool: pool},
		Accounts:  principal.PostgresDirectory{Pool: pool, Logger: logger},
		Errors:    errors,
		Logger:    logger,
	}
	if err := handoff.Check(); err != nil {
		return err
	}
	handoff.Register(api)
	searchconnectors.ConnectionDeleteRoute{
		Connections: connectorRows,
		Removals:    connectorRows,
		Documents:   removal,
		Revocation:  revocation,
		Tools:       toolChanges,
		Analytics:   writeAnalytics,
		Errors:      errors,
		Logger:      logger,
	}.Register(api)
	searchconnectors.ServerDeleteRoute{
		Changes: connectorRows,
		Tools:   toolChanges,
		Errors:  errors,
		Logger:  logger,
	}.Register(api)
	// The account's switch for one connection. Off keeps the connection and
	// its documents; the chat and the indexing schedule leave it alone.
	searchconnectors.EnabledRoute{
		Changes: connectorRows,
		Tools:   toolChanges,
		Errors:  errors,
		Logger:  logger,
	}.Register(api)
	// One type serves both edits of a server's trusted tools; Remove is
	// what makes the second the untrust.
	for _, untrust := range []bool{false, true} {
		searchconnectors.TrustRoute{
			Remove:  untrust,
			Changes: connectorRows,
			Tools:   toolChanges,
			Errors:  errors,
			Logger:  logger,
		}.Register(api)
	}

	// Adding, changing and testing an MCP server, and the generic create and
	// update of a connection (which is also how the settings page edits a
	// server). All of them decide where a server may be with the guard the
	// chat calls it through, given the same list of site hosts, so that what
	// can be saved can be called.
	serverGuard := mcptools.PublicInternet{Resolve: mcptools.SystemResolve, ConnectTimeout: chat.ConnectedAppConnectTimeout}
	serverPolicy := searchconnectors.ServerPolicy{
		Standing:           connectorRows,
		SiteHostsWhenUnset: chat.ConnectedAppSiteHosts(),
		Destinations:       serverGuard,
	}
	serverProbe := mcptools.Probe{
		Network:    serverGuard,
		ClientName: settings.BrandName,
		// One test: connect, list, hang up. The form waits on it.
		Timeout:   20 * time.Second,
		MostTools: 200,
	}
	if err := serverProbe.Check(); err != nil {
		return err
	}
	searchconnectors.PolicyRoute{Policy: serverPolicy, Errors: errors, Logger: logger}.Register(api)
	searchconnectors.ServerCreateRoute{
		Writes: connectorRows, Policy: serverPolicy, Tools: toolChanges, Errors: errors, Logger: logger,
	}.Register(api)
	searchconnectors.ServerUpdateRoute{
		Rows: connectorRows, Writes: connectorRows, Policy: serverPolicy, Tools: toolChanges, Errors: errors, Logger: logger,
	}.Register(api)
	searchconnectors.ServerTestRoute{
		Rows: connectorRows, Policy: serverPolicy, Probe: serverProbe, Errors: errors, Logger: logger,
	}.Register(api)
	searchconnectors.ConnectionCreateRoute{
		Writes: connectorRows, Now: time.Now, Errors: errors, Logger: logger,
	}.Register(api)
	searchconnectors.ConnectionUpdateRoute{
		Rows: connectorRows, Writes: connectorRows, Policy: serverPolicy, Tools: toolChanges,
		Now: time.Now, Errors: errors, Logger: logger,
	}.Register(api)

	// The Nextcloud import: the preview of a pick, the import, and the
	// refresh of one imported file (internal/nextcloudimport). Mounted only
	// when the deployment names its Nextcloud server; without one there is
	// no server a connection could be used against, and the three paths
	// answer the router's 404.
	//
	// An import commits its documents and then downloads their files in a
	// pool in this process, which is why the pool is stopped during
	// shutdown, after the listener has closed (stopDownloads, at the end of
	// this function): a file it could not finish is marked failed with a
	// reason that says to import again.
	stopDownloads := func(context.Context) error { return nil }
	// Which documents can be refreshed from Nextcloud. Mounted either way:
	// the document list asks it in every deployment, and where there is no
	// Nextcloud the true answer is "none".
	refreshable := nextcloudimport.RefreshableRoute{
		Rows: nextcloudimport.RefreshableStore{Pool: pool}, Errors: errors, Logger: logger,
	}
	if nextcloudCredentials != nil {
		refreshable.ServerURL = nextcloudCredentials.ServerURL
	}
	refreshable.Register(api)
	if nextcloudCredentials != nil {
		connect, err := nextcloud.NewConnect(providers, nextcloud.Timing{
			Listing: nextcloudListing, Stall: writeTimeout, MaxRetryWait: nextcloudMaxRetryWait,
		})
		if err != nil {
			return err
		}
		importFiles, err := fileStores.Writer()
		if err != nil {
			return err
		}
		importRows := nextcloudimport.PostgresRows{Pool: pool}
		// The pool's lifetime is the signal context: a signal ends every
		// download at once, and Stop then waits for each to record that.
		downloads, err := nextcloudimport.NewDownloads(ctx, settings.NextcloudDownloadConcurrency, settings.UploadMaxFileBytes,
			importRows, importFiles,
			// The same queue and marker an upload's index job goes through.
			indexqueue.Indexing{Queue: queue, Marker: indexqueue.PostgresMarker{Pool: pool}, Logger: logger},
			connect, logger)
		if err != nil {
			return err
		}
		stopDownloads = downloads.Stop
		nextcloudimport.Routes{
			Rows:         importRows,
			Parsers:      parsers,
			Vision:       uploadVision,
			Credentials:  *nextcloudCredentials,
			Connect:      connect,
			Downloads:    downloads,
			MaxFileBytes: settings.UploadMaxFileBytes,
			Errors:       errors,
			Logger:       logger,
		}.Register(api)
	} else {
		logger.Info("no Nextcloud server configured; the Nextcloud import routes are not mounted")
	}

	// Notebooks: nine routes -- a notebook's own row and the account's edits
	// of the report templates (internal/notebooks, whose package comment
	// lists them and says what is still to be written). The two that read
	// a body are in `withBodies` further down (notebooks.WithBodies).
	// `GET /notebooks/search` is the search across notebooks, registered
	// with the chat's pieces below as a literal path, which ServeMux
	// prefers to the notebook whose id would be "search".
	//
	// Every statement is one guarded statement on the pool, so the routes
	// take the pool itself. Now is left nil: nil is the system clock, and
	// only a test names another.
	notebooks.Routes{
		Database: pool,
		Errors:   errors,
		Logger:   logger,
	}.Register(api)
	// A notebook's sources: the list, the add, the two toggles and the
	// removal, each confined to the caller's notebook, documents and
	// folders (internal/notebooksources). They read their own small JSON
	// bodies, so they are not in withBodies.
	notebooksources.Routes{Pool: pool, Errors: errors, Logger: logger, Naming: queue}.Register(api)
	// A notebook's studio outputs: the list, the rename and the delete,
	// which stops the output's run and removes its artifact with the same
	// removal every document delete uses (internal/studiooutputs).
	studiooutputs.Routes{Pool: pool, Removal: removal, Errors: errors, Logger: logger}.Register(api)
	// The stop button on a deliverable being made (internal/deliverablejobs).
	deliverablejobs.CancelRoute{Database: pool, Errors: errors, Logger: logger}.Register(api)
	// The job card's read, which the phone polls when it is not on Zero.
	deliverablejobs.ReadRoute{Database: pool, Errors: errors, Logger: logger}.Register(api)
	// And its Retry button: the job's next attempt, queued and published as
	// the chat first published it, so the worker is told in one spelling.
	retryJob := deliverablejobs.RetryRoute{
		Database: pool, Publish: deliverablejobs.PublishVideo(queue, deliverablejobs.RetryPath),
		Errors: errors, Logger: logger,
	}
	if err := retryJob.Check(); err != nil {
		return err
	}
	retryJob.Register(api)

	// Podcasts: five routes -- the detail, the three lifecycle transitions
	// that need neither a model nor the billing ledger, and the delete
	// (internal/podcasts). Only the approval takes the queue, because only
	// the approval starts a draft; the regeneration and the revert move a row
	// and nothing else.
	//
	// The voice catalogue and the six routes that need it are further down,
	// after these five. `/podcasts/voices` and `/podcasts/languages` are
	// literal patterns, so ServeMux prefers them to `/podcasts/{podcast_id}`
	// and nothing needs handing back to Python any more.
	podcastRows := podcasts.Store{Pool: pool}
	podcasts.DetailRoute{
		Podcasts: podcastRows,
		Errors:   errors,
		Logger:   logger,
	}.Register(api)
	podcasts.NewApproveRoute(podcastRows, queue, errors, logger).Register(api)
	podcasts.NewRegenerateRoute(podcastRows, errors, logger).Register(api)
	podcasts.NewRevertRoute(podcastRows, errors, logger).Register(api)
	podcasts.DeleteRoute{
		Podcasts: podcastRows,
		Errors:   errors,
		Logger:   logger,
	}.Register(api)

	// The speech side of podcasts: which voices exist and which languages they
	// speak, the preview of one voice, creating a podcast, editing its brief,
	// and cancelling it (soloclef_assistant/voices, internal/podcasts).
	//
	// The catalogue used to live inside the Python process. Here it is the
	// fixed rosters as data plus one question to the LiteLLM proxy for an
	// OpenAI-style service, cached per process. voices.New refuses to start on
	// a setting it cannot make sense of, because the alternative -- quietly
	// offering a different service's voices -- is the hidden fallback this
	// project does not allow.
	//
	// Previews are the one place that spends money on a provider, so each
	// voice is synthesised once per deployment and kept in Redis, shared by
	// every replica; the sentence and model are fixed on the server so the
	// route cannot be used as a free speech API.
	voiceSettings := voices.Settings{
		Service:       settings.TTSService,
		APIBase:       settings.TTSServiceAPIBase,
		APIKey:        settings.TTSServiceAPIKey,
		PodcastVoices: settings.TTSPodcastVoices,
	}
	// Deadlines come from each call's context, not from the client.
	speechProxy := voices.NewProxy(voiceSettings, &http.Client{})
	catalogue, err := voices.New(voiceSettings, speechProxy, logger)
	if err != nil {
		return err
	}
	sampleStore, err := voices.NewValkeySamples(settings.RedisURL)
	if err != nil {
		return err
	}
	defer sampleStore.Close()
	samples := voices.NewSamples(catalogue, speechProxy, sampleStore, logger)

	podcasts.VoicesRoute{Voices: catalogue, Errors: errors, Logger: logger}.Register(api)
	podcasts.LanguagesRoute{Voices: catalogue, Errors: errors, Logger: logger}.Register(api)
	podcasts.PreviewRoute{
		Voices:  catalogue,
		Samples: samples,
		Errors:  errors,
		Logger:  logger,
	}.Register(api)
	podcasts.CreateRoute{Podcasts: podcastRows, Voices: catalogue, Errors: errors, Logger: logger}.Register(api)
	podcasts.BriefRoute{Podcasts: podcastRows, Voices: catalogue, Errors: errors, Logger: logger}.Register(api)
	// Billing is cut from the product, so cancel releases no hold: it moves the
	// podcast to cancelled from the status it was decided on, and nothing else.
	podcasts.NewCancelRoute(podcastRows, errors, logger).Register(api)

	health.Route{Database: pool, Logger: logger}.Register(api)

	// The chat: asking a question and streaming the answer, resuming a turn
	// that paused for an approval, asking a question again, stopping a turn,
	// the two reads a page makes when it reloads mid-answer, and reading and
	// choosing the account's chat model (internal/chat).
	//
	// Unlike every route above, a turn outlives the function that started
	// it: it runs in a goroutine of its own so that a browser closing its
	// tab does not stop the answer being written. That is what the three
	// deferred calls are for, and their order is the point. Deferred calls
	// run last-in first-out, so on the way out of run, by whichever return:
	//
	//  1. endChat cancels the context every turn runs under. A turn told to
	//     stop saves what it has so far.
	//  2. chatService.Wait blocks until each has finished saving.
	//  3. closeChat releases what NewService opened (its Valkey client).
	//
	// and only after those does the pool close, because all three were
	// deferred after it. The context is one of this block's own, derived
	// from the signal context, rather than the signal context itself: run
	// also returns without a signal (a listener that could not bind, an
	// error further down this function), and then nothing would have told
	// the turns to stop and Wait would wait for them for ever.
	//
	// The file store is asked for its writer here, at start-up, so that a
	// deployment whose configured backend cannot be written to is told now
	// and not by the first answer that tries to save a document.
	chatFiles, err := fileStores.Writer()
	if err != nil {
		return err
	}
	chatLifetime, endChat := context.WithCancel(ctx)
	defer endChat()
	// The start-up context bounds the one thing NewService does with it:
	// creating the loop's checkpoint table if it is not there.
	// What the chat's knowledge-base specialist changes notes, documents
	// and folders with, and what the undo routes take a change back with:
	// one value, so both index through the same queue and remove passages
	// from the same collection (internal/knowledgebase).
	knowledgeChanges := knowledgebase.Changes{
		Pool:     pool,
		Indexing: indexqueue.Indexing{Queue: queue, Marker: indexqueue.PostgresMarker{Pool: pool}, Logger: logger},
		Vectors:  documentVectors,
		Logger:   logger,
	}
	chatService, closeChat, err := chat.NewService(startup, chat.Settings{
		ModelURL:           settings.LiteLLMURL,
		ModelKey:           settings.LiteLLMKey,
		ChatModel:          settings.ChatModel,
		EmbeddingModel:     settings.EmbeddingModel,
		EmbeddingDimension: settings.EmbeddingDimension,
		QdrantURL:          settings.QdrantURL,
		QdrantAPIKey:       settings.QdrantAPIKey,
		QdrantCollection:   settings.QdrantChunkCollection,
		QdrantHNSWEf:       settings.QdrantSearchHNSWEf,
		QdrantQuantized:    settings.QdrantQuantized,
		QdrantRescore:      settings.QdrantSearchRescore,
		QdrantOversampling: settings.QdrantSearchOversampling,
		NCBITool:           settings.NCBITool,
		NCBIEmail:          settings.NCBIEmail,
		NCBIAPIKey:         settings.NCBIAPIKey,
		// Running turns are coordinated across replicas in the same Valkey
		// the limiter counts in; the brand is the one the refusals name.
		ValkeyURL: settings.RedisURL,
		Brand:     settings.BrandName,
		MaxSteps:  settings.ChatMaxSteps,
		MaxTurn:   settings.ChatMaxTurn,
		// What a public link to a thread starts with: the web app's address.
		FrontendURL: settings.FrontendURL,
		// What the chat's mail and Drive tools open an account's stored
		// credentials with, and where each kind is renewed
		// (connected_accounts.go).
		SecretKey:     settings.SessionSecret,
		MicrosoftMail: chatMicrosoftMail(settings),
		GoogleDrive:   chatGoogleDrive(settings),
		// The Nextcloud the import reaches, which the chat's Nextcloud tools
		// reach with the same stored app password; none means no tools.
		NextcloudURL: settings.NextcloudBaseURL,
		// The models that draw and that look at pictures, each off when its
		// name is empty; and the sandbox documents are built in, which is
		// read from the environment only when its switch is on
		// (internal/config), so with it off this is the zero value and the
		// chat never looks at it.
		ImageModel:     settings.ChatImageModel,
		VisionModel:    settings.ChatVisionModel,
		SandboxEnabled: settings.SandboxEnabled,
		Sandbox: sandbox.Settings{
			Stack:          settings.SandboxStack,
			URL:            settings.SandboxURL,
			APIKey:         settings.SandboxAPIKey,
			Image:          settings.SandboxImage,
			CPU:            settings.SandboxCPU,
			Memory:         settings.SandboxMemory,
			IdleTTL:        settings.SandboxIdleTTL,
			CommandTimeout: settings.SandboxCommandTimeout,
			MaxPerAccount:  settings.SandboxMaxPerAccount,
		},
	}, chat.Deps{
		Pool:  pool,
		Files: chatFiles,
		Queue: queue,
		// No Timeout, on purpose, and NewService refuses a client that has
		// one: an answer streams for longer than any whole-request limit
		// that would suit a search. Each call is bounded by its turn's
		// context instead, which CHAT_MAX_TURN_SECONDS ends.
		HTTP: &http.Client{},
		// The catalogue the podcast routes above were given, not a second
		// one: a podcast asked for in chat is proposed voices from the same
		// roster the Podcasts page offers.
		Voices:   catalogue,
		Logger:   logger,
		Lifetime: chatLifetime,
		// The chat's own product events, through the capture every other
		// write of this tier uses: nothing when the deployment has no
		// project key.
		Events: writeAnalytics,
		// The registry uploads are read with: a file a chat tool fetches
		// is read by the parser its connection chose, as an upload is.
		Parsers: parsers,
		// The specialist that changes the account's documents and folders.
		KnowledgeBase: knowledgeChanges,
	})
	if err != nil {
		return err
	}
	defer closeChat()
	defer chatService.Wait()
	defer endChat()
	chat.Routes{Service: chatService, Errors: errors}.Register(api)
	// The account's models for its other jobs (quick work, studio work,
	// looking at pictures, drawing them) and whether it can chat at all.
	// They are chosen from the same read of the proxy the chat's model
	// menu is drawn from, which is why they are mounted here, after it.
	// The chat holds its catalogue as the one list it needs; the pickers
	// also need the proxy's image models, which the same catalogue reads.
	proxyLists, listsBoth := chatService.Models.Catalogue.(modelchoices.Lists)
	if !listsBoth {
		return fmt.Errorf("the chat's model catalogue (%T) does not list the proxy's image models", chatService.Models.Catalogue)
	}
	modelChoices := modelchoices.Choices{
		Lists:         proxyLists,
		Stored:        modelchoices.Rows{Pool: pool},
		ChatDefault:   settings.ChatModel,
		VisionDefault: settings.ChatVisionModel,
		ImageDefault:  settings.ChatImageModel,
		// The account's own connections' models, the same the chat's menu
		// offers (soloclef_assistant/ownmodels).
		Own: chatService.Models.Own,
	}
	if err := modelChoices.Check(); err != nil {
		return err
	}
	modelchoices.Register(api, modelChoices, errors, logger)
	// The check of chat instructions before they are saved: fixed patterns,
	// and a reading by the account's quick model through the proxy.
	instructionslint.Route{
		QuickModel: func(ctx context.Context, userID string) (string, error) {
			return modelChoices.ModelFor(ctx, userID, modelchoices.Quick)
		},
		Open:         chatService.Models.OpenFor,
		ModelTimeout: instructionsLintTimeout,
		Errors:       errors,
		Logger:       logger,
	}.Register(api)
	// A thread's own routes: its row, its messages as the apps load them,
	// its public links, and the composer's tool list. They are served by
	// the service the chat built, because deleting a thread stops its turn
	// and removes the loop's conversation of it (internal/chat/threads).
	threads.Routes{Service: chatService.Threads, Errors: errors}.Register(api)

	// The routes that run on the chat's own pieces (chat.Parts): one model
	// menu, one document search, one set of academic sources and one
	// artifact specialist in the process, shared and never built twice.
	//
	// A notebook's follow-up questions and source guides are written by the
	// account's chat model; the search across notebooks is the chat's
	// hybrid search held to the documents the caller's notebooks read.
	notebookfollowups.Route{
		Notebooks: notebookfollowups.New(pool),
		Models:    chatService.Parts.Models,
		Errors:    errors,
		Logger:    logger,
	}.Register(api)
	sourceguides.Route{
		Guides: sourceguides.New(pool),
		Models: chatService.Parts.Models,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	notebooksearch.Route{
		Notebooks: notebooksearch.New(pool),
		Search:    chatService.Parts.Documents,
		Errors:    errors,
		Logger:    logger,
	}.Register(api)
	// PubMed, arXiv and the preprint servers, each its own route and its
	// own search, as the MCP server's tools call them. Google Scholar has
	// no source in this tier and no route.
	academicsearch.Routes{Sources: chatService.Parts.Academic, Errors: errors, Logger: logger}.Register(api)
	// The search over the caller's own documents by meaning and by words,
	// which is the MCP server's knowledge-base search: the chat's search,
	// asked as the caller.
	documentsearch.Route{Pool: pool, Search: chatService.Parts.Documents, Errors: errors, Logger: logger}.Register(api)

	// A notebook's research box: which sources the account can search, the
	// search itself (a stream, paced like the byte streams), and bringing
	// what was found in as sources (internal/notebookresearch). It starts
	// on PubMed; every source is asked and reported on its own, never
	// blended. An imported page is read in this process, a few at a time
	// across all imports, which is why the reads are stopped at shutdown
	// beside the Nextcloud downloads (stopResearchReads, at the end of this
	// function); the signal context ends each read at once.
	// A PDF an import finds is kept where an upload's file is, under the
	// upload's per-file limit, and indexed as an uploaded PDF.
	researchFiles, err := fileStores.Writer()
	if err != nil {
		return err
	}
	researchReads, err := notebookresearch.NewReads(ctx, researchConcurrentReads, pool, chatService.Parts.Pages, chatService.Parts.Academic,
		indexqueue.Indexing{Queue: queue, Marker: indexqueue.PostgresMarker{Pool: pool}, Logger: logger},
		notebookresearch.Originals{Files: researchFiles, MaxBytes: settings.UploadMaxFileBytes}, logger)
	if err != nil {
		return err
	}
	stopResearchReads := researchReads.Stop
	research := notebookresearch.Routes{
		Pool:    pool,
		Papers:  chatService.Parts.Academic,
		Web:     chatService.Parts.Web,
		Pages:   chatService.Parts.Pages,
		Reads:   researchReads,
		Parsers: parsers,
		Stall:   writeTimeout,
		Errors:  errors,
		Logger:  logger,
	}
	if err := research.Check(); err != nil {
		return err
	}
	research.Register(api)

	// The studio: starting an output and the ideas its dialogs offer
	// (internal/studiogenerate). Audio and video go through the
	// specialist's own podcast opener and video queuer; every other kind is
	// the specialist itself, run with nobody watching.
	//
	// A run outlives its request, as a turn does, so the runner is stopped
	// the way the chat is. Deferred calls run last-in first-out: endChat
	// (again; it is idempotent) cancels the context the runs are under,
	// then Wait blocks until each has written its ending, and both happen
	// before the chat's own three and the pool's close, which were deferred
	// earlier. The sweep fails the jobs of a process that died.
	studioRunner, err := studiogenerate.NewRunner(pool, chatService.Parts.Models, chatService.Parts.Deliverables,
		studioConcurrentRuns, chatLifetime, logger)
	if err != nil {
		return err
	}
	defer studioRunner.Wait()
	defer endChat()
	studioRunner.Sweep()
	// The opener as the chat built it, so that the studio can open a
	// podcast in the shape its dialog chose (voices and length) through
	// the very same rows and voice catalogue.
	podcastOpener, isOpener := chatService.Parts.Deliverables.Podcasts.(deliverables.Podcasts)
	if !isOpener {
		return fmt.Errorf("the chat's podcast opener is a %T, which the studio cannot shape a brief for", chatService.Parts.Deliverables.Podcasts)
	}
	studio := studiogenerate.Routes{
		Pool:   pool,
		Models: chatService.Parts.Models,
		Runner: studioRunner,
		OpenPodcast: func(ctx context.Context, owner deliverables.Owner, brief deliverables.PodcastBrief, request deliverables.PodcastRequest) (int64, error) {
			opener := podcastOpener
			opener.Brief = brief
			return opener.Open(ctx, owner, request)
		},
		StartDraft: func(ctx context.Context, userID string, podcastID int64, requestID string) error {
			return podcasts.StartDraft(ctx, podcastRows, queue, userID, podcastID,
				celeryqueue.LogContextFor(studiogenerate.GeneratePath, requestID, userID))
		},
		Videos:         chatService.Parts.Deliverables.Videos,
		SandboxEnabled: settings.SandboxEnabled,
		// The image model is optional (SOLOCLEF_API_CHAT_IMAGE_MODEL): a
		// deployment that named none cannot draw an infographic.
		ImageEnabled: settings.ChatImageModel != "",
		Errors:       errors,
		Logger:       logger,
	}
	if err := studio.Check(); err != nil {
		return err
	}
	studio.Register(api)
	// A finished output's viewer frame and a report's reading view with
	// its suggestions (internal/studioviews). The suggestions are written
	// by the account's quick model through the proxy; an accepted one is
	// started by the studio itself, so there is one way an output begins.
	outputViews := studioviews.Routes{
		Pool: pool,
		QuickModel: func(ctx context.Context, userID string) (string, error) {
			return modelChoices.ModelFor(ctx, userID, modelchoices.Quick)
		},
		Open:         chatService.Models.OpenFor,
		ModelTimeout: reportSuggestionsTimeout,
		Studio:       studio,
		Errors:       errors,
		Logger:       logger,
	}
	if err := outputViews.Check(); err != nil {
		return err
	}
	outputViews.Register(api)
	// Signed links for the audio and video inside an interactive report
	// (internal/medialinks). The page asks for them in a session; the link
	// itself is opened by a media element that sends no credential, so it
	// is served outside the gate and the signature is its authorisation.
	mediaLinks := medialinks.Routes{
		Pool: pool, Files: fileStores, Secret: settings.SessionSecret,
		StallTimeout: writeTimeout, Errors: errors, Logger: logger,
	}
	if err := mediaLinks.Check(); err != nil {
		return err
	}
	mediaLinks.Register(api)
	// An account's automations: saved here, run by the worker. Run now
	// creates the run's row and publishes its message through the queue
	// every other job of this tier is published on; "can this account run
	// one" is asked of the chat's own model menu, because a run is answered
	// by the model the account's chat is.
	automationRoutes := automations.Routes{
		Pool: pool, Queue: queue, Models: chatService.Parts.Models, Errors: errors, Logger: logger, Now: time.Now,
		// An automation may ask for its result to be emailed only where
		// this tier sends mail; the mail itself is automation_mail.go's.
		SendsMail: settings.SMTPEnabled,
	}
	if err := automationRoutes.Check(); err != nil {
		return err
	}
	automationRoutes.Register(api)
	// The results of runs whose automation asked for an email, sent by this
	// tier because it holds the SMTP client (automation_mail.go).
	if err := startAutomationMail(ctx, settings, pool, logger); err != nil {
		return err
	}

	// The inbox, the agent's feature flags as this tier really has them,
	// and a thread's action log (empty until the chat's tools write it; the
	// two undo routes wait for that and are not served).
	notifications.Routes{Pool: pool, Errors: errors, Logger: logger}.Register(api)
	// The administrator's announcements, each delivered as an inbox row
	// to every active account (internal/siteannouncements).
	siteannouncements.Routes{Pool: pool, Errors: errors, Logger: logger}.Register(api)
	// The address "Report Issue" mails go to, for anyone: one setting,
	// and no address at all when it is empty (internal/supportcontact).
	supportContact := supportcontact.Route{Email: settings.SupportEmail}
	if err := supportContact.Check(); err != nil {
		return err
	}
	supportContact.Register(api)
	// What "Connect your agent" shows a signed-in person to paste into
	// claude.ai or ChatGPT: the agent application's client id and secret,
	// from the same settings the token verifier is built from.
	agentsignin.SetupRoute{
		Settings: agentsignin.Settings{
			Issuer: settings.AgentIssuer, ClientID: settings.AgentClientID, ProviderName: settings.AgentProviderName,
		},
		ClientSecret: settings.AgentClientSecret,
		Errors:       errors,
		Logger:       logger,
	}.Register(api)
	agentflags.Route{
		Disabled:          settings.DisableNewAgentStack,
		PermissionEnabled: settings.PermissionEnabled,
		Errors:            errors,
		Logger:            logger,
	}.Register(api)
	agentactions.Route{Pool: pool, Errors: errors, Logger: logger}.Register(api)
	// Undoing what the assistant changed: one action, or a turn's.
	knowledgebase.Routes{Changes: knowledgeChanges, Errors: errors}.Register(api)

	gate := principal.Gate{
		Resolver: principal.Resolver{
			SessionTokens: principal.SessionToken{
				Secret:      settings.SessionSecret,
				MinIssuedAt: settings.MinIssuedAt,
			},
			Directory:         principal.PostgresDirectory{Pool: pool, Logger: logger},
			Agents:            agentPrincipals(settings, pool, logger),
			SessionCookieName: settings.SessionCookieName,
		},
		Errors: errors,
		Logger: logger,
	}

	// Every method-and-path pair this API answers. Three things need them: the
	// counter, to count only requests for a route that exists; the analytics
	// hook, to record the route template rather than the path; and the body
	// reader, to buffer only the requests that carry one.
	served := servedRoutes()
	// The routes that read a request body. Each is buffered and checked before
	// the credential is looked at, because that is the order FastAPI decides in
	// (internal/requestbody).
	withBodies := []string{
		http.MethodPost + " " + accesstokens.CreatePath,
		http.MethodPost + " " + activitylogs.CreatePath,
		http.MethodPut + " " + activitylogs.UpdatePath,
		http.MethodPut + " " + markdownmemory.UserWritePath,
		http.MethodPost + " " + folders.CreatePath,
		http.MethodPut + " " + folders.RenamePath,
		http.MethodPut + " " + folders.MovePath,
		http.MethodPut + " " + folders.ReorderPath,
		http.MethodPost + " " + prompts.CreatePath,
		http.MethodPut + " " + prompts.UpdatePath,
		http.MethodPut + " " + folders.DocumentMovePath,
		http.MethodPut + " " + folders.BulkMovePath,
		http.MethodPatch + " " + accountprofile.UpdatePath,
		http.MethodPatch + " " + accountprofile.SettingsUpdatePath,
	}
	// The three note routes that read a body, from the package's own list.
	withBodies = append(withBodies, notes.WithBodies()...)
	// The five notebook routes that read a body, as the package lists them:
	// appended rather than spelled out here, so that the package's own list
	// is the one place that says which of its routes take one.
	withBodies = append(withBodies, notebooks.WithBodies()...)
	// Creating a podcast and editing its brief are the two podcast routes that
	// read a body.
	withBodies = append(withBodies,
		http.MethodPost+" "+podcasts.CreatePath,
		http.MethodPatch+" "+podcasts.BriefPath,
	)
	// The five artifact study writes that read a body. The sixth, the
	// flashcard reset, is a DELETE that takes its generation in the query.
	withBodies = append(withBodies,
		http.MethodPatch+" "+artifacts.FlashcardProgressPath,
		http.MethodPut+" "+artifacts.FlashcardOrderPath,
		http.MethodPut+" "+artifacts.QuizAnswerPath,
		http.MethodPut+" "+artifacts.QuizSkipPath,
		http.MethodPost+" "+artifacts.QuizRetakePath,
	)

	// Asking a question, answering a paused turn's approvals, asking a
	// question again and choosing the account's model are the four chat
	// routes that read a body; stopping a turn is a POST without one.
	withBodies = append(withBodies,
		http.MethodPost+" "+chat.TurnPath,
		http.MethodPost+" "+chat.ResumePath,
		http.MethodPost+" "+chat.RegeneratePath,
		http.MethodPut+" "+chat.ChatModelPath,
	)

	// The thread routes that read a body, from the package's own table.
	withBodies = append(withBodies, threads.BodyRoutes()...)
	// The model settings that read a body: a connection's writes and tests,
	// the account's model choices, and the instructions check.
	withBodies = append(withBodies, modelconnections.BodyRoutes()...)
	withBodies = append(withBodies, modelchoices.BodyRoutes()...)
	withBodies = append(withBodies, http.MethodPost+" "+instructionslint.Path)
	// Writing an agent permission rule and changing one, likewise.
	withBodies = append(withBodies, agentrules.BodyRoutes()...)

	// The connection and MCP-server writes read a small JSON body through
	// the buffered reader, from the package's own list. The uploads, the
	// connector parser's PUT and the three Nextcloud routes are deliberately
	// NOT here: an upload must be streamed, and the others read their own
	// body with their own limit.
	withBodies = append(withBodies, searchconnectors.BodyRoutes()...)
	// Adding a mailbox and testing unsaved mailbox settings, likewise.
	withBodies = append(withBodies, mailaccounts.BodyRoutes()...)
	// Starting a Drive connect, whose body is optional.
	withBodies = append(withBodies, driveconnect.BodyRoutes()...)
	// Starting a Microsoft mailbox connect. (The Drive import is not here:
	// it reads its own body, with its own limit.)
	withBodies = append(withBodies, microsoftmail.BodyRoutes()...)
	// Saving and changing an automation and its triggers.
	withBodies = append(withBodies, automations.WithBodies()...)

	counter, err := ratelimit.NewValkeyCounter(settings.RedisURL)
	if err != nil {
		return err
	}
	defer counter.Close()
	limiter := ratelimit.Middleware{
		Counter: &ratelimit.Storage{
			Shared:   counter,
			Fallback: &ratelimit.MemoryCounter{},
			Logger:   logger,
		},
		Limit: ratelimit.DefaultLimit,
		// Only the routes this API answers: a request for a path no route
		// matches is a 404 that costs nothing, so it spends no budget.
		Covers: ratelimit.CoveringRoutes(served...),
		Errors: errors,
		Logger: logger,
	}
	// The web app's crash reports, one log line each (internal/clienterrors).
	// Registered here because its two budgets are counted in the limiter's
	// own counter, so they are one budget across every replica.
	crashReports := clienterrors.Route{Budget: limiter.Counter, Errors: errors, Logger: logger}
	if err := crashReports.Check(); err != nil {
		return err
	}
	crashReports.Register(api)

	// The probe is the one path that answers without a credential; everything
	// else is counted and then passes the gate. The order is the Python tier's:
	// its limiter is outside its router, so a request is counted before anyone
	// knows who sent it, and the probe is exempt there too (@limiter.exempt).
	// Inside the gate, because the event is only recorded for a caller the
	// gate resolved and found gated.
	answer := gate.Wrap(api.Adapter())
	if events != nil {
		answer = gate.Wrap(gatedanalytics.Middleware{
			Sink:     &gatedanalytics.PostHogSink{Queue: events},
			Template: gatedanalytics.RouteTemplates(served...),
		}.Wrap(api.Adapter()))
	}
	// Outside the gate on purpose: a body that is not JSON is refused before
	// the credential is looked at, the order clients have always seen
	// (internal/requestbody). Being outside the gate is also why it bounds
	// what it buffers (requestbody.MaxBufferedBytes): a stranger's body costs
	// memory here.
	answer = requestbody.Middleware{
		Covers: requestbody.CoveringRoutes(withBodies...),
		Errors: errors,
	}.Wrap(answer)

	// Between the CORS policy and the limiter, which is where
	// CsrfOriginMiddleware sits in app.py's own stack: a cookie-authenticated
	// write from a foreign origin is refused before it is counted, and the
	// refusal still gets its CORS headers. A sign-in, refresh or sign-out from
	// a foreign page is refused with no cookie as well (forgery_policy.go).
	forgeries := forgeryPolicy(settings.SessionCookieName,
		settings.CSRFAllowedOrigins, settings.FrontendURL, settings.PublicURL)

	handler, err := frontDoor{
		Logger:    logger,
		CORS:      corsmirror.NewPolicy(settings.AllowedOrigins),
		Forgeries: forgeries,
		Limiter:   limiter,
		// A password is checked at most ten times a minute from one
		// client, on each of the two sign-in routes: the one control that
		// matters against guessing, beside the general limit above.
		Attempts: ratelimit.Middleware{
			Counter: limiter.Counter,
			Limit:   ratelimit.Limit{Amount: 10, Multiples: 1, Granularity: "minute", Window: time.Minute},
			Covers:  ratelimit.CoveringRoutes(signin.PasswordAttempts()...),
			Errors:  errors,
			Logger:  logger,
		},
		Ungated:  api.Adapter(),
		Gated:    answer,
		Served:   served,
		TestOnly: testOnlyRoutes,
	}.handler()
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              settings.ListenAddress,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       120 * time.Second,
	}

	failed := make(chan error, 1)
	go func() {
		logger.Info("serving", slog.String("address", settings.ListenAddress))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			failed <- err
		}
	}()

	select {
	case err := <-failed:
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down", slog.Duration("grace", settings.ShutdownGrace))
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), settings.ShutdownGrace)
	defer cancelShutdown()
	if err := server.Shutdown(shutdown); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	// After the listener has closed, so no new import can start a download
	// while the pool is being stopped; within the same grace. What could
	// not be stopped in time is logged and the process still exits: those
	// documents are queued again by the next import that names them.
	if err := stopDownloads(shutdown); err != nil {
		logger.Error("stopping the Nextcloud downloads", slog.String("error", err.Error()))
	}
	// Likewise the research box's page reads: each records how it ended,
	// and one cut short is marked to be imported again.
	if err := stopResearchReads(shutdown); err != nil {
		logger.Error("stopping the research page reads", slog.String("error", err.Error()))
	}
	logger.Info("stopped")
	return nil
}

// modelTestTimeout bounds one test of a model connection, and
// instructionsLintTimeout the model's reading of chat instructions. Both
// are waited on by a person on a settings page, with an answer that is
// useful without the model (a verdict that says "did not answer in time",
// the pattern warnings alone), so each is seconds and not a turn's minutes.
const (
	modelTestTimeout        = 30 * time.Second
	instructionsLintTimeout = 10 * time.Second
	// A report's suggestions are a longer answer than a lint: the model
	// reads the report's outline and proposes outputs, twice at most.
	reportSuggestionsTimeout = 30 * time.Second
	// How long the proxy's catalogue of providers' models is kept before
	// it is read again. It changes when the proxy is upgraded.
	modelCatalogueFresh = time.Hour
)

func newLogger(level string) *slog.Logger {
	levels := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: levels[strings.ToLower(level)],
	}))
}

// agentPrincipals is the agent-token verifier, or nothing when this deployment
// names no agent provider — in which case nothing can verify an agent's token
// and the gate answers it 401.
func agentPrincipals(settings config.Config, pool *pgxpool.Pool, logger *slog.Logger) principal.Agents {
	agents := agentsignin.Settings{
		Issuer:       settings.AgentIssuer,
		ClientID:     settings.AgentClientID,
		ProviderName: settings.AgentProviderName,
	}
	if !agents.Enabled() {
		logger.Info("no agent provider configured; agent tokens are refused (401)")
		return nil
	}
	return agentsignin.Principals{
		Verifier: &agentsignin.Verifier{
			Settings:  agents,
			Directory: &agentsignin.ProviderDirectory{DiscoveryURL: agents.DiscoveryURL()},
		},
		Accounts:  agentsignin.PostgresAccounts{Pool: pool, ProviderName: agents.ProviderName},
		BrandName: settings.BrandName,
		Logger:    logger,
	}
}

// How long this tier waits for Nextcloud, beside the stall limit (which is
// writeTimeout's figure). They are properties of the protocol and of what a
// person will wait for, not of a deployment.
const (
	// nextcloudListing bounds one PROPFIND, start to finish: a folder of
	// ten thousand entries answers in a few seconds, and a picker that has
	// waited this long is better told to try again.
	nextcloudListing = 30 * time.Second
	// nextcloudMaxRetryWait caps how long one "slow down" answer from
	// Nextcloud is obeyed for, so a Retry-After of an hour cannot hold a
	// download slot for an hour.
	nextcloudMaxRetryWait = 30 * time.Second
)

// writeTimeout is how long a client may keep a response waiting.
//
// It is used in two ways, which is why it has a name. As the http.Server's
// WriteTimeout it bounds a whole response, measured from the moment the
// request's headers were read: every JSON answer is held to it. The routes
// that stream a file cannot be -- a video on a slow link takes minutes, and
// was cut mid-body at exactly this many seconds -- so they are handed the
// same number as a limit on *stalling* instead, and move the connection's
// deadline forward as the client takes bytes (internal/rollingdeadline). One
// number, so that "how long may a client that has stopped reading hold a
// connection" has one answer whichever kind of route it is on.
const writeTimeout = 60 * time.Second

// studioConcurrentRuns is how many studio artifacts one process makes at
// once; the rest wait their turn as "queued". A run is a model conversation
// and, for a report or a deck, a sandbox: four keeps a busy afternoon from
// taking the model proxy's whole rate limit while one person's quiz still
// starts at once.
const studioConcurrentRuns = 4

// researchConcurrentReads is how many imported pages one process reads at
// once, across every account's imports: enough that a batch of ten links is
// in within a minute, few enough that it is never a crawl.
const researchConcurrentReads = 4

// frontDoor is everything between the listener and a route: the mux, and the
// middlewares that wrap the whole server. It is a type of its own, rather
// than lines in run, so that cmd/api's tests can assemble *this* wiring around
// stand-in routes and hold it to the answers clients rely on
// (router_test.go). The order below is the thing under test.
type frontDoor struct {
	Logger    *slog.Logger
	CORS      corsmirror.Policy
	Forgeries csrforigin.Policy
	Limiter   ratelimit.Middleware
	// Attempts is the tighter limit on the routes that check a password
	// (signin.PasswordAttempts). Its zero value limits nothing, which is
	// what router_test.go's stand-in routes want.
	Attempts ratelimit.Middleware
	// Ungated answers the paths that take no credential: the health probe and
	// every pair in publicRoutes().
	Ungated http.Handler
	// Gated answers everything else, and is the credential gate with the
	// routes behind it.
	Gated http.Handler
	// Served is servedRoutes(): what the path guard decides against.
	Served []string
	// TestOnly is the end-to-end test routes, as "METHOD /path" pairs, in
	// a stack that switched them on; nil everywhere else, and then no such
	// path is mounted (internal/e2eseed).
	TestOnly []string
}

func (d frontDoor) handler() (http.Handler, error) {
	// Read first, because it is the one step that can refuse: a served route
	// the path guard cannot reason about stops the server from starting.
	paths, err := starlettepath.NewServed(d.Served...)
	if err != nil {
		return nil, err
	}

	routes := http.NewServeMux()
	routes.Handle(health.Path, d.Ungated)
	// The routes of this tier that answer a request with no credential
	// (publicRoutes, which says of each why it has none). None of their
	// Python counterparts takes `get_auth_context`, so there is no principal
	// to resolve and the gate would 401 every request — which is why each
	// pattern is mounted here rather than under "/".
	//
	// It is not the probe's wiring, though it sits beside it. The probe is
	// `@limiter.exempt` in app.py and none of these is, so SlowAPI counts
	// each against the client's minute there and the limiter counts it here
	// — which is also why every one of them is in servedRoutes(). CSRF
	// is in the chain for the order's sake and decides nothing: it checks
	// unsafe methods and each of these is a GET. What is deliberately left
	// out is the
	// gate and, with it, the gated-request analytics inside it — the Python
	// middleware reads `request.state.auth_context`, which only
	// `get_auth_context` sets, so an uncredentialed request records nothing
	// on either tier.
	//
	// The method is named in each pattern so that any other method on the
	// path falls through to "/" and so to the gate: a write under a public
	// prefix (`POST /api/v1/public/{share_token}/clone`) takes a longer path
	// or another method, matches none of these patterns, and needs a
	// credential like any other write.
	public := d.Ungated
	if d.Attempts.Covers != nil {
		public = d.Attempts.Wrap(public)
	}
	ungated := d.Forgeries.Middleware(d.Limiter.Wrap(public))
	for _, pair := range publicRoutes() {
		routes.Handle(pair, ungated)
	}
	// Outside the limiter as well as the gate: a suite of many workers
	// asks for a token far more often than a person signs in. Each still
	// refuses a request without the stack's seed secret.
	for _, pair := range d.TestOnly {
		routes.Handle(pair, d.Forgeries.Middleware(d.Ungated))
	}
	routes.Handle("/", d.Forgeries.Middleware(d.Limiter.Wrap(d.Gated)))

	// In front of the mux, because both things it settles are decided by the
	// mux before any handler runs: ServeMux answers a path with an empty
	// segment by redirecting it, and it segments an escaped slash differently
	// from Starlette's router, which matters before the gate's 401 can be
	// allowed to speak (internal/starlettepath.Guard, which also says exactly
	// which requests it changes: none whose path the two routers already
	// agree on, so not the probe's and not an ordinary served or forwarded
	// request's).
	//
	// Where it sits among the middlewares is the Python tier's order for the
	// router's own 404, read off app.py; router_test.go holds the server to
	// each of the four:
	//
	//   - **inside** the request id and the CORS policy: the bare 404 carries
	//     both, because Starlette's router is inside CORSMiddleware.
	//   - **behind** the CSRF check: CsrfOriginMiddleware is outside the
	//     router, so a cookie-authenticated write from a foreign origin to a
	//     path no route matches is the 403 there, not the 404.
	//   - **not counted**: SlowAPI counts a request against the route it
	//     matched, and this is the answer for matching none.
	//   - **before the gate**: the router runs before any dependency, so a
	//     request with no credential is the 404 and not a 401.
	guarded := starlettepath.Guard{
		Served:   paths,
		NotFound: d.Forgeries.Middleware(starlettepath.NotFound()),
	}.Wrap(routes)

	return requestcontext.Middleware(d.Logger, d.CORS.Middleware(guarded)), nil
}

// publicRoutes is the served routes that take no credential, as "METHOD /path"
// pairs. frontDoor mounts exactly these outside the credential gate, and
// nothing else decides that a route is public: a pair missing from here is
// behind the gate and answers 401 to a stranger, and a pair added here is
// answered to anybody. Each is also in servedRoutes(), because the rate
// limiter counts them as the Python tier's SlowAPI does.
func publicRoutes() []string {
	routes := []string{
		// The three public share-link reads. The share token in the path is
		// the whole of the authorisation; a credential sent with one is never
		// looked at, on either tier (internal/publicchat).
		http.MethodGet + " " + publicchat.PagePath,
		http.MethodGet + " " + publicchat.VideoPath,
		http.MethodGet + " " + publicchat.PodcastPath,
	}
	// The sign-in routes: nobody is signed in yet, or the access token has
	// expired and the refresh token in the request is the proof
	// (internal/signin).
	routes = append(routes, signin.Public()...)
	// What a share link serves to whoever holds it: a snapshot link's
	// files and streams, a notebook's live page and its files. The token in
	// the path is the whole of the authorisation (internal/publicshare,
	// internal/notebooklinks).
	routes = append(routes, publicshare.Public()...)
	routes = append(routes, notebooklinks.Public()...)
	// A signed media link inside a report: the signature is the proof.
	routes = append(routes, medialinks.Public()...)
	// The support address, shown on pages that have no session.
	routes = append(routes, supportcontact.Public()...)
	// How people can sign in here: asked before anyone has
	// (internal/signinoptions).
	routes = append(routes, signinoptions.Public()...)
	// Google's callback of a Drive connect: its proof is the attempt's
	// sealed cookie, not a session (internal/driveconnect).
	routes = append(routes, driveconnect.Public()...)
	// Microsoft's callback of a mailbox connect, on the same terms
	// (internal/microsoftmail).
	routes = append(routes, microsoftmail.Public()...)
	// The phone app's hand-off page, proved by the one-time code in its
	// path, and its Picker page's import, proved by the ticket in its body
	// (internal/apphandoff).
	routes = append(routes, apphandoff.Public()...)
	// Single sign-on's two: the browser arrives from the sign-in page, and
	// then from the provider, signed in to nothing (internal/oidcsignin).
	return append(routes, oidcsignin.Public()...)
}

// servedRoutes is every route this API answers: the one list that has to
// be exhaustive, because the rate limiter, the analytics hook, the body
// reader and the path guard are all built from it (served_test.go checks it
// holds each route once).
func servedRoutes() []string {
	routes := []string{
		http.MethodGet + " " + activitysummary.Path,
		http.MethodPost + " " + accesstokens.CreatePath,
		http.MethodGet + " " + accesstokens.ListPath,
		http.MethodDelete + " " + accesstokens.RevokePath,
		http.MethodGet + " " + artifacts.ListPath,
		http.MethodGet + " " + artifacts.VideoPath,
		http.MethodDelete + " " + artifacts.DeletePath,
		http.MethodGet + " " + artifacts.ManifestPath,
		http.MethodGet + " " + artifacts.DownloadPath,
		http.MethodGet + " " + artifacts.FileContentPath,
		http.MethodGet + " " + artifacts.SlideAudioPath,
		http.MethodPatch + " " + artifacts.FlashcardProgressPath,
		http.MethodDelete + " " + artifacts.FlashcardProgressPath,
		http.MethodPut + " " + artifacts.FlashcardOrderPath,
		http.MethodPut + " " + artifacts.QuizAnswerPath,
		http.MethodPut + " " + artifacts.QuizSkipPath,
		http.MethodPost + " " + artifacts.QuizRetakePath,
		http.MethodGet + " " + activitylogs.ReadPath,
		http.MethodPost + " " + activitylogs.CreatePath,
		http.MethodPut + " " + activitylogs.UpdatePath,
		http.MethodDelete + " " + activitylogs.DeletePath,
		http.MethodGet + " " + markdownmemory.UserReadPath,
		http.MethodPut + " " + markdownmemory.UserWritePath,
		http.MethodPost + " " + markdownmemory.UserResetPath,
		http.MethodGet + " " + folders.ListPath,
		http.MethodGet + " " + folders.ReadPath,
		http.MethodGet + " " + folders.BreadcrumbPath,
		http.MethodPost + " " + folders.CreatePath,
		http.MethodPut + " " + folders.RenamePath,
		http.MethodPut + " " + folders.MovePath,
		http.MethodPut + " " + folders.ReorderPath,
		http.MethodPatch + " " + folders.WatchedPath,
		http.MethodDelete + " " + folders.DeletePath,
		// GET /api/v1/prompts/public is a literal beside a GET of the list and
		// three routes under /prompts/{prompt_id}; there is deliberately no
		// GET of one prompt, here or there, so a GET of /prompts/7 is the
		// Python tier's 405 and never reaches this tier
		// (internal/prompts.ReadRoutes).
		http.MethodGet + " " + prompts.ListPath,
		http.MethodGet + " " + prompts.PublicPath,
		http.MethodPost + " " + prompts.CreatePath,
		http.MethodPut + " " + prompts.UpdatePath,
		http.MethodDelete + " " + prompts.DeletePath,
		http.MethodPost + " " + prompts.CopyPath,
		http.MethodPut + " " + folders.DocumentMovePath,
		http.MethodPut + " " + folders.BulkMovePath,
		http.MethodGet + " " + publiclinks.ListPath,
		http.MethodDelete + " " + publiclinks.RevokePath,
		http.MethodGet + " " + accountprofile.ReadPath,
		http.MethodPatch + " " + accountprofile.UpdatePath,
		http.MethodDelete + " " + accountprofile.DeletePath,
		http.MethodGet + " " + accountprofile.SettingsReadPath,
		http.MethodPatch + " " + accountprofile.SettingsUpdatePath,
		// The documents slice, the citation route first.
		http.MethodGet + " " + documents.ByChunkPath,
		http.MethodGet + " " + documents.ChunksPath,
		http.MethodGet + " " + documents.VersionsPath,
		http.MethodGet + " " + documents.VersionPath,
		http.MethodPost + " " + documents.RestorePath,
		http.MethodGet + " " + documents.StatusPath,
		http.MethodGet + " " + documents.TypeCountsPath,
		http.MethodGet + " " + documents.WatchedFoldersPath,
		http.MethodGet + " " + documents.TitlesPath,
		http.MethodGet + " " + documentcontent.Path,
		// Public, and counted all the same: the list is what the rate
		// limiter counts against, and a public read is no exception
		// (publicRoutes).
		http.MethodGet + " " + publicchat.PagePath,
		http.MethodGet + " " + publicchat.VideoPath,
		http.MethodGet + " " + publicchat.PodcastPath,
		http.MethodGet + " " + modelconnections.ProvidersPath,
		http.MethodGet + " " + modelconnections.ConnectionsPath,
		// Five of the search-connector reads. The MCP server's delete and the
		// two trust routes are absent on purpose; the registration block in
		// run says what they wait for.
		http.MethodGet + " " + searchconnectors.ListPath,
		http.MethodGet + " " + searchconnectors.ServersPath,
		http.MethodGet + " " + searchconnectors.OnePath,
		http.MethodGet + " " + searchconnectors.ServerPath,
		// The four that change an account's connections, held back until
		// the chat's MCP cache could be invalidated across processes.
		http.MethodDelete + " " + searchconnectors.OnePath,
		http.MethodDelete + " " + searchconnectors.ServerPath,
		http.MethodPost + " " + searchconnectors.TrustPath,
		http.MethodPost + " " + searchconnectors.UntrustPath,
		http.MethodPut + " " + searchconnectors.EnabledPath,
		http.MethodPost + " " + searchconnectors.ListPath,
		http.MethodPut + " " + searchconnectors.OnePath,
		http.MethodPost + " " + searchconnectors.ServersPath,
		http.MethodPut + " " + searchconnectors.ServerPath,
		http.MethodPost + " " + searchconnectors.ServerTestPath,
		http.MethodGet + " " + searchconnectors.ServerPolicyPath,
		http.MethodGet + " " + nextcloudconnect.ConnectionPath,
		http.MethodPost + " " + nextcloudconnect.LoginPath,
		http.MethodPost + " " + nextcloudconnect.PollPath,
		http.MethodDelete + " " + nextcloudconnect.CancelPath,
		http.MethodDelete + " " + nextcloudconnect.DisconnectPath,
		http.MethodGet + " " + mailaccounts.AccountsPath,
		http.MethodPost + " " + mailaccounts.AccountsPath,
		http.MethodDelete + " " + mailaccounts.AccountPath,
		http.MethodPost + " " + mailaccounts.AccountTestPath,
		http.MethodPost + " " + mailaccounts.TestPath,
		// A Microsoft mailbox's sign-in: whether it is offered, the start,
		// and Microsoft's callback (which takes no credential: publicRoutes).
		http.MethodGet + " " + microsoftmail.AvailabilityPath,
		http.MethodPost + " " + microsoftmail.ConnectPath,
		http.MethodGet + " " + microsoftmail.CallbackPath,
		http.MethodGet + " " + driveconnect.ConnectionPath,
		http.MethodPost + " " + driveconnect.ConnectPath,
		http.MethodGet + " " + driveconnect.AddPath,
		http.MethodGet + " " + driveconnect.ReauthPath,
		http.MethodGet + " " + driveconnect.CallbackPath,
		http.MethodGet + " " + driveconnect.PickerPath,
		http.MethodDelete + " " + driveconnect.DisconnectPath,
		// Importing picked Drive files, and "sync now" on a connection.
		http.MethodPost + " " + drivesync.ImportPath,
		http.MethodPost + " " + drivesync.IndexPath,
		// The Nextcloud picker's two reads (mounted with the import, below)
		// and which documents can be refreshed (mounted always).
		http.MethodGet + " " + nextcloudimport.BrowsePath,
		http.MethodGet + " " + nextcloudimport.SearchPath,
		http.MethodGet + " " + nextcloudimport.RefreshablePath,
		// The Nextcloud import. Mounted only when the deployment names its
		// Nextcloud server, and listed either way: the list is what the
		// limiter counts against, and a deployment without Nextcloud answers
		// these three with the router's 404.
		http.MethodPost + " " + nextcloudimport.PreviewPath,
		http.MethodPost + " " + nextcloudimport.ImportPath,
		http.MethodPost + " " + nextcloudimport.RefreshPath,
		// The podcast actions. DetailPath and DeletePath are one path with
		// two methods.
		http.MethodGet + " " + podcasts.DetailPath,
		http.MethodPost + " " + podcasts.ApprovePath,
		http.MethodPost + " " + podcasts.RegeneratePath,
		http.MethodPost + " " + podcasts.RevertPath,
		http.MethodDelete + " " + podcasts.DeletePath,
		http.MethodGet + " " + podcasts.VoicesPath,
		http.MethodGet + " " + podcasts.LanguagesPath,
		http.MethodGet + " " + podcasts.PreviewPath,
		http.MethodPost + " " + podcasts.CreatePath,
		http.MethodPatch + " " + podcasts.BriefPath,
		http.MethodPost + " " + podcasts.CancelPath,
		// The chat: the turn, its resume and a regenerated turn stream an
		// answer, the stop ends a running turn, and the two reads are what
		// a reloaded page asks.
		http.MethodPost + " " + chat.TurnPath,
		http.MethodPost + " " + chat.ResumePath,
		http.MethodPost + " " + chat.RegeneratePath,
		http.MethodPost + " " + chat.StopPath,
		http.MethodGet + " " + chat.StatusPath,
		http.MethodGet + " " + chat.PendingPath,
		// The caller's chat model: one path, read and chosen.
		http.MethodGet + " " + chat.ChatModelPath,
		http.MethodPut + " " + chat.ChatModelPath,
	}
	// The thirteen thread routes, from the table they are mounted from.
	routes = append(routes, threads.AllRoutes()...)
	// The model settings: a connection's six writes and tests, the seven
	// routes of an account's model choices, and the instructions check.
	routes = append(routes, modelconnections.WriteRouteList()...)
	routes = append(routes, modelchoices.Routes()...)
	routes = append(routes, http.MethodPost+" "+instructionslint.Path)
	// The four agent permission rule routes, likewise.
	routes = append(routes, agentrules.AllRoutes()...)
	// The seven routes of a document's life, from the package that mounts
	// them, for the reason the notebooks' list is taken from theirs.
	routes = append(routes, documentlifecycle.Served()...)
	// The ten note routes, from the package's own list.
	routes = append(routes, notes.Served()...)
	// The fifteen notebook routes, from the package's own list for the
	// reason withBodies takes its list from there.
	routes = append(routes, notebooks.Served()...)
	routes = append(routes, notebooksources.Served()...)
	routes = append(routes, studiooutputs.Served()...)
	routes = append(routes, studiogenerate.Served()...)
	// The notebook routes that ask a model or the search, and the academic
	// search routes, each from its package's own list.
	routes = append(routes, notebookfollowups.Served()...)
	routes = append(routes, sourceguides.Served()...)
	routes = append(routes, notebooksearch.Served()...)
	routes = append(routes, academicsearch.Served()...)
	routes = append(routes, notebookresearch.Served()...)
	// The document library, a document's stored files and the search by
	// meaning.
	routes = append(routes, documentlibrary.Served()...)
	routes = append(routes, documentfiles.Served()...)
	routes = append(routes, documentsearch.Served()...)
	// The inbox, the agent flags, a thread's action log and its undo.
	routes = append(routes, notifications.Served()...)
	routes = append(routes, agentflags.Served()...)
	routes = append(routes, agentactions.Served()...)
	routes = append(routes, knowledgebase.Served()...)
	// An account's automations, their triggers and their runs.
	routes = append(routes, automations.Served()...)
	routes = append(routes, http.MethodPost+" "+deliverablejobs.CancelPath, http.MethodPost+" "+deliverablejobs.RetryPath,
		http.MethodGet+" "+deliverablejobs.ReadPath)
	// The phone's push registration and choices, and the sign-in options.
	routes = append(routes, pushdevices.Served()...)
	routes = append(routes, signinoptions.Served()...)
	// The task log list, the site's own model connections, the studio's
	// views and media links, both kinds of share link, the notebook copy,
	// the announcements, the crash reports and the support address.
	routes = append(routes, http.MethodGet+" "+activitylogs.ListPath)
	routes = append(routes, modelconnections.GlobalRouteList()...)
	routes = append(routes, studioviews.Served()...)
	routes = append(routes, medialinks.Served()...)
	routes = append(routes, publicshare.Served()...)
	routes = append(routes, notebooklinks.Served()...)
	routes = append(routes, notebookcopy.Served()...)
	routes = append(routes, siteannouncements.Served()...)
	routes = append(routes, clienterrors.Served()...)
	routes = append(routes, supportcontact.Served()...)
	// What an AI agent's sign-in form needs pasted.
	routes = append(routes, agentsignin.SetupServed()...)
	// The phone app's browser hand-off (internal/apphandoff).
	routes = append(routes, apphandoff.ServedRoutes()...)
	// The sign-in routes and the session read, from the package's own list.
	routes = append(routes, signin.Served()...)
	// Single sign-on's two routes.
	return append(routes, oidcsignin.Served()...)
}
