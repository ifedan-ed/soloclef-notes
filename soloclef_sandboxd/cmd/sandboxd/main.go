// Command sandboxd is the stack's sandbox service (the module's go.mod says
// what it is). `sandboxd` serves; `sandboxd health` is the container's health
// probe, so the image needs no curl.
//
// Every setting is required and has no default (settings.go).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_sandboxd/api"
	"git.danvics.com/danvics/soloclef/soloclef_sandboxd/docker"
	"git.danvics.com/danvics/soloclef/soloclef_sandboxd/sandboxes"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if len(os.Args) == 2 && os.Args[1] == "health" {
		os.Exit(probe())
	}
	if err := serve(log); err != nil {
		log.Error("sandboxd: stopped", "error", err)
		os.Exit(1)
	}
}

func serve(log *slog.Logger) error {
	settings, err := load(os.Getenv)
	if err != nil {
		return err
	}
	key, err := readKey(settings.KeyFile)
	if err != nil {
		return err
	}
	if err := publishKey(key, settings.KeyCopy); err != nil {
		return err
	}

	engine := docker.New(settings.DockerSocket)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if err := engine.Ping(startup); err != nil {
		return err
	}
	// The image is checked once here so a stack started without building it
	// says so at once; it is named by reference on every create, so a
	// rebuilt image is used from the next sandbox on with no restart.
	if err := engine.ImageExists(startup, settings.Image); err != nil {
		return fmt.Errorf("the sandbox image %s: %w", settings.Image, err)
	}
	manager, err := sandboxes.New(engine, settings.Stack, sandboxes.Image{
		Reference: settings.Image, Path: settings.Path, User: settings.User,
	}, log)
	if err != nil {
		return err
	}
	if err := manager.Adopt(startup); err != nil {
		return err
	}
	log.Info("sandboxd: ready", "stack", settings.Stack, "image", settings.Image, "adopted", manager.Count())
	go manager.SweepUntil(ctx)

	server := &http.Server{
		Addr:    settings.Listen,
		Handler: api.Handler(manager, engine, key, log),
		// No read or write timeout: a render's command holds its request
		// for many minutes and a video file streams out for as long as it
		// takes. Each call is bounded by its own limit or its caller.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	served := make(chan error, 1)
	go func() { served <- server.ListenAndServe() }()
	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}
	// Sandboxes are left running: the next start adopts them, so a restart
	// of this service costs a chat none of its files. Calls in flight are
	// given a moment to answer.
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := server.Shutdown(shutdown); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}

// probe is the health check: the service answers and Docker answers it.
func probe() int {
	settings, err := load(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	client := &http.Client{Timeout: 4 * time.Second}
	response, err := client.Get("http://127.0.0.1" + settings.Listen + "/health")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		fmt.Fprintln(os.Stderr, "health:", response.Status)
		return 1
	}
	return 0
}
