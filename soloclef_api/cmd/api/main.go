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

	"git.danvics.com/danvics/surfsense/soloclef_api/internal/config"
	"git.danvics.com/danvics/surfsense/soloclef_api/internal/corsmirror"
	"git.danvics.com/danvics/surfsense/soloclef_api/internal/database"
	"git.danvics.com/danvics/surfsense/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/surfsense/soloclef_api/internal/health"
	"git.danvics.com/danvics/surfsense/soloclef_api/internal/logsummary"
	"git.danvics.com/danvics/surfsense/soloclef_api/internal/principal"
	"git.danvics.com/danvics/surfsense/soloclef_api/internal/pythondelegate"
	"git.danvics.com/danvics/surfsense/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/surfsense/soloclef_api/internal/workspaceaccess"
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

	api := humago.New(http.NewServeMux(), huma.DefaultConfig("Soloclef API", "0.1.0"))
	logsummary.Route{
		Logs:   logsummary.Reader{Pool: pool},
		Access: workspaceaccess.PostgresReader{Pool: pool},
		Errors: errors,
		Logger: logger,
	}.Register(api)
	health.Route{Database: pool, Logger: logger}.Register(api)

	gate := principal.Gate{
		Resolver: principal.Resolver{
			SessionTokens: principal.SessionToken{
				Secret:      settings.SessionSecret,
				MinIssuedAt: settings.MinIssuedAt,
			},
			Directory:         principal.PostgresDirectory{Pool: pool, Logger: logger},
			SessionCookieName: settings.SessionCookieName,
		},
		Errors:     errors,
		PythonTier: pythonTier,
		Logger:     logger,
	}

	// The probe is the one path that answers without a credential; everything
	// else passes the gate first.
	routes := http.NewServeMux()
	routes.Handle(health.Path, api.Adapter())
	routes.Handle("/", gate.Wrap(api.Adapter()))

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
