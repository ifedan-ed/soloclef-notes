// Command api serves the routes Soloclef's Python API has handed to Go.
//
// It is one process with one job: answer the routes it owns exactly as the
// Python tier answered them, and hand everything else back. It is started with
// no routes of its own reachable from the internet until the proxy is told to
// send a path here (custom/docs/go-api-tier.md).
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

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/accesstokens"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/agentsignin"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/celeryqueue"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/config"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/corsmirror"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/csrforigin"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/documentdeletion"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/folders"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/gatedanalytics"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/health"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/logsummary"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/markdownmemory"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/myaccess"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/permissioncatalog"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/posthogcapture"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythondelegate"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/ratelimit"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/routeanalytics"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/workspaceaccess"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/workspacedeletion"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/workspacelogs"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/workspacemembers"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/workspaceroles"
)

func main() {
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

	errors := errorenvelope.Writer{ReportURL: settings.ErrorReportURL}

	pythonTier, err := pythondelegate.New(settings.PythonUpstream, logger,
		func(response http.ResponseWriter, request *http.Request, _ error) {
			errors.Write(response, http.StatusBadGateway,
				"The API could not be reached right now. Please try again.",
				requestcontext.IDFrom(request))
		})
	if err != nil {
		return err
	}

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

	api := humago.New(http.NewServeMux(), huma.DefaultConfig("Soloclef API", "0.1.0"))
	access := workspaceaccess.PostgresReader{Pool: pool}
	logsummary.Route{
		Logs:   logsummary.Reader{Pool: pool},
		Access: access,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	workspacemembers.Route{
		Members: workspacemembers.Reader{Pool: pool},
		Access:  access,
		Errors:  errors,
		Logger:  logger,
	}.Register(api)
	myaccess.Route{
		Access: access,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	workspaceroles.ReadRoutes{
		Roles:  workspaceroles.Store{Pool: pool},
		Access: access,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	workspaceroles.CreateRoute{
		Pool:   pool,
		Access: access,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	workspaceroles.UpdateRoute{
		Pool:   pool,
		Access: access,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	workspaceroles.DeleteRoute{
		Pool:   pool,
		Access: access,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	permissioncatalog.Route{Errors: errors, Logger: logger}.Register(api)
	accesstokens.Routes{
		Pool:      pool,
		Tokens:    accesstokens.Store{Pool: pool},
		Analytics: writeAnalytics,
		Errors:    errors,
		Logger:    logger,
	}.Register(api)
	documentdeletion.Route{
		Pool:   pool,
		Access: access,
		Queue:  queue,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	workspacedeletion.Route{
		Pool:   pool,
		Access: access,
		Queue:  queue,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	logStore := workspacelogs.Store{Pool: pool}
	workspacelogs.ReadRoute{
		Logs:   logStore,
		Access: access,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	workspacelogs.CreateRoute{
		Pool:   pool,
		Access: access,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	workspacelogs.UpdateRoute{
		Pool:   pool,
		Access: access,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	workspacelogs.DeleteRoute{
		Pool:   pool,
		Access: access,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	memory := markdownmemory.Store{Pool: pool}
	markdownmemory.UserRoutes{
		Pool:   pool,
		Memory: memory,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	markdownmemory.TeamRoutes{
		Pool:   pool,
		Memory: memory,
		Access: access,
		Errors: errors,
		Logger: logger,
	}.Register(api)
	folderStore := folders.Store{Pool: pool}
	folders.ReadRoutes{
		Folders: folderStore,
		Access:  access,
		Errors:  errors,
		Logger:  logger,
	}.Register(api)
	folders.CreateRoute{
		Pool:    pool,
		Folders: folderStore,
		Access:  access,
		Errors:  errors,
		Logger:  logger,
	}.Register(api)
	folders.RenameRoute{
		Pool:    pool,
		Folders: folderStore,
		Access:  access,
		Errors:  errors,
		Logger:  logger,
	}.Register(api)
	folders.MoveRoute{
		Pool:    pool,
		Folders: folderStore,
		Access:  access,
		Errors:  errors,
		Logger:  logger,
	}.Register(api)
	folders.ReorderRoute{
		Pool:    pool,
		Folders: folderStore,
		Access:  access,
		Errors:  errors,
		Logger:  logger,
	}.Register(api)
	folders.WatchRoute{
		Pool:    pool,
		Folders: folderStore,
		Access:  access,
		Errors:  errors,
		Logger:  logger,
	}.Register(api)
	folders.DeleteRoute{
		Pool:    pool,
		Folders: folderStore,
		Access:  access,
		Queue:   queue,
		Errors:  errors,
		Logger:  logger,
	}.Register(api)
	health.Route{Database: pool, Logger: logger}.Register(api)

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
		Errors:     errors,
		PythonTier: pythonTier,
		Logger:     logger,
	}

	// Every method-and-path pair this tier answers. Three things need them: the
	// counter, to count only what the Python tier would have counted; the
	// analytics hook, to record the route template rather than the path; and
	// the body reader, to buffer only the requests that carry one.
	served := []string{
		http.MethodGet + " " + logsummary.Path,
		http.MethodGet + " " + workspacemembers.Path,
		http.MethodGet + " " + myaccess.Path,
		http.MethodGet + " " + workspaceroles.ListPath,
		http.MethodGet + " " + workspaceroles.GetPath,
		http.MethodPost + " " + workspaceroles.CreatePath,
		http.MethodPut + " " + workspaceroles.UpdatePath,
		http.MethodDelete + " " + workspaceroles.DeletePath,
		http.MethodGet + " " + permissioncatalog.Path,
		http.MethodGet + " " + accesstokens.ListPath,
		http.MethodDelete + " " + accesstokens.RevokePath,
		http.MethodDelete + " " + documentdeletion.Path,
		http.MethodDelete + " " + workspacedeletion.Path,
		http.MethodGet + " " + workspacelogs.ReadPath,
		http.MethodPost + " " + workspacelogs.CreatePath,
		http.MethodPut + " " + workspacelogs.UpdatePath,
		http.MethodDelete + " " + workspacelogs.DeletePath,
		http.MethodGet + " " + markdownmemory.UserReadPath,
		http.MethodPut + " " + markdownmemory.UserWritePath,
		http.MethodPost + " " + markdownmemory.UserResetPath,
		http.MethodGet + " " + markdownmemory.TeamReadPath,
		http.MethodPut + " " + markdownmemory.TeamWritePath,
		http.MethodPost + " " + markdownmemory.TeamResetPath,
		http.MethodGet + " " + folders.ReadPath,
		http.MethodGet + " " + folders.BreadcrumbPath,
		http.MethodPost + " " + folders.CreatePath,
		http.MethodPut + " " + folders.RenamePath,
		http.MethodPut + " " + folders.MovePath,
		http.MethodPut + " " + folders.ReorderPath,
		http.MethodPatch + " " + folders.WatchedPath,
		http.MethodDelete + " " + folders.DeletePath,
	}
	// The folder writes, which are the ones the git knowledge store would have
	// a say in (internal/folders.StoreDelegate).
	folderWrites := []string{
		http.MethodPost + " " + folders.CreatePath,
		http.MethodPut + " " + folders.RenamePath,
		http.MethodPut + " " + folders.MovePath,
		http.MethodPut + " " + folders.ReorderPath,
		http.MethodPatch + " " + folders.WatchedPath,
		http.MethodDelete + " " + folders.DeletePath,
	}
	// The routes that read a request body. Each is buffered and checked before
	// the credential is looked at, because that is the order FastAPI decides in
	// (internal/requestbody).
	withBodies := []string{
		http.MethodPost + " " + workspaceroles.CreatePath,
		http.MethodPut + " " + workspaceroles.UpdatePath,
		http.MethodPost + " " + workspacelogs.CreatePath,
		http.MethodPut + " " + workspacelogs.UpdatePath,
		http.MethodPut + " " + markdownmemory.UserWritePath,
		http.MethodPut + " " + markdownmemory.TeamWritePath,
		http.MethodPost + " " + folders.CreatePath,
		http.MethodPut + " " + folders.RenamePath,
		http.MethodPut + " " + folders.MovePath,
		http.MethodPut + " " + folders.ReorderPath,
	}

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
		// Only the routes this tier answers, because the Python tier's limiter
		// counts a request only against a route of its own that matches it.
		Covers: ratelimit.CoveringRoutes(served...),
		Errors: errors,
		Logger: logger,
	}

	// The probe is the one path that answers without a credential; everything
	// else is counted and then passes the gate. The order is the Python tier's:
	// its limiter is outside its router, so a request is counted before anyone
	// knows who sent it, and the probe is exempt there too (@limiter.exempt).
	// Inside the gate, because the event is only recorded for a caller this
	// tier resolved and found gated, and a request it forwards is recorded by
	// the tier that answers it.
	answer := gate.Wrap(api.Adapter())
	if events != nil {
		answer = gate.Wrap(gatedanalytics.Middleware{
			Sink:     &gatedanalytics.PostHogSink{Queue: events},
			Template: gatedanalytics.RouteTemplates(served...),
		}.Wrap(api.Adapter()))
	}
	// Outside the gate on purpose: a body the Python tier refuses before it
	// solves its dependencies is refused before the credential there too.
	answer = requestbody.Middleware{
		Covers:    requestbody.CoveringRoutes(withBodies...),
		Elsewhere: pythonTier,
	}.Wrap(answer)

	// Outside the body reader as well as the gate: a folder write this tier
	// will not serve must reach the Python tier exactly as it arrived, with
	// its own body, and be answered there from the beginning. Nothing of this
	// tier has read or written anything by then, which is what makes
	// forwarding a *write* safe (internal/folders.StoreDelegate).
	answer = folders.StoreDelegate{
		Enabled:   settings.KnowledgeStoreEnabled,
		Covers:    requestbody.CoveringRoutes(folderWrites...),
		Elsewhere: pythonTier,
	}.Wrap(answer)

	// Between the CORS policy and the limiter, which is where
	// CsrfOriginMiddleware sits in app.py's own stack: a cookie-authenticated
	// write from a foreign origin is refused before it is counted, and the
	// refusal still gets its CORS headers.
	forgeries := csrforigin.NewPolicy(settings.SessionCookieName,
		settings.CSRFAllowedOrigins, settings.FrontendURL, settings.PublicURL)

	routes := http.NewServeMux()
	routes.Handle(health.Path, api.Adapter())
	routes.Handle("/", forgeries.Middleware(limiter.Wrap(answer)))

	cors := corsmirror.NewPolicy(settings.AllowedOrigins)
	server := &http.Server{
		Addr:              settings.ListenAddress,
		Handler:           requestcontext.Middleware(logger, cors.Middleware(routes)),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
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
	logger.Info("stopped")
	return nil
}

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
// names no agent provider — in which case an agent's request is forwarded to the
// Python tier exactly as it was before this tier could verify one.
func agentPrincipals(settings config.Config, pool *pgxpool.Pool, logger *slog.Logger) principal.Agents {
	agents := agentsignin.Settings{
		Issuer:       settings.AgentIssuer,
		ClientID:     settings.AgentClientID,
		ProviderName: settings.AgentProviderName,
	}
	if !agents.Enabled() {
		logger.Info("no agent provider configured; agent tokens are served by the Python tier")
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
