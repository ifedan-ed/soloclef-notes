package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

// service is one long-running part of this process: consuming, scheduling, or
// serving the scrape.
type service struct {
	name string
	run  func(ctx context.Context) error
}

// together runs the services until one of them returns, then stops the rest
// and returns everything that failed.
//
// One returning stops the others on purpose. A metrics server whose socket
// died, or a scheduler that cannot reach Redis, is not something to keep a
// half-running worker for: the restart is the fix, and a worker that looks
// alive while part of it is not is worse than one that is down.
func together(ctx context.Context, services ...service) error {
	ctx, stop := context.WithCancel(ctx)
	defer stop()

	var (
		mu       sync.Mutex
		failures []error
		wait     sync.WaitGroup
	)
	for _, svc := range services {
		wait.Add(1)
		go func(svc service) {
			defer wait.Done()
			defer stop()
			if err := svc.run(ctx); err != nil {
				slog.Error("a part of the worker stopped", "part", svc.name, "error", err)
				mu.Lock()
				failures = append(failures, err)
				mu.Unlock()
			}
		}(svc)
	}
	wait.Wait()
	return errors.Join(failures...)
}
