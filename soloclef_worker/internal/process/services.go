// Package process holds what every command of this module does the same way:
// find the broker, name itself, and run its long-lived parts together. It
// exists because there are two commands (cmd/worker, which runs Python task
// bodies, and cmd/indexer, which indexes in Go) and neither may quietly
// diverge from the other on which Redis it consumes from.
package process

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

// Service is one long-running part of this process: consuming, scheduling, or
// serving the scrape.
type Service struct {
	Name string
	Run  func(ctx context.Context) error
}

// Together runs the services until one of them returns, then stops the rest
// and returns everything that failed.
//
// One returning stops the others on purpose. A metrics server whose socket
// died, or a scheduler that cannot reach Redis, is not something to keep a
// half-running worker for: the restart is the fix, and a worker that looks
// alive while part of it is not is worse than one that is down.
func Together(ctx context.Context, services ...Service) error {
	ctx, stop := context.WithCancel(ctx)
	defer stop()

	var (
		mu       sync.Mutex
		failures []error
		wait     sync.WaitGroup
	)
	for _, svc := range services {
		wait.Add(1)
		go func(svc Service) {
			defer wait.Done()
			defer stop()
			if err := svc.Run(ctx); err != nil {
				slog.Error("a part of the worker stopped", "part", svc.Name, "error", err)
				mu.Lock()
				failures = append(failures, err)
				mu.Unlock()
			}
		}(svc)
	}
	wait.Wait()
	return errors.Join(failures...)
}
