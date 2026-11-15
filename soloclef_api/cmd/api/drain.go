package main

import (
	"context"
	"log/slog"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/inflight"
)

// workDrain is how long a stopping API waits for the work it has in flight --
// Studio artifacts being made and chat answers being written -- before it
// stops what is left.
//
// Five minutes, from the jobs this site has run (deliverable_jobs on the live
// database, 2026-11-14): an artifact that was made took 45 seconds at the
// median, 236 at the 90th percentile and 249 at most; the longest run of any
// outcome was 390 seconds, and that one failed at its step limit. So every
// artifact the site has ever finished would have finished inside five
// minutes even had it started the moment the deploy began, and a chat
// answer is shorter than an artifact. Longer would only hold a deploy for
// the rare run that is going nowhere; the specialist's own limit is twenty
// minutes, which no deploy should wait out.
//
// What is still running when it passes is not lost: a Studio job is queued
// again and made by the next process (studiogenerate, requeue.go), and a
// chat answer is saved as far as it got.
//
// The drain ends as soon as nothing is in flight, so a deploy with nothing
// running is not held at all. docker/docker-compose.go.yml gives the API a
// stop_grace_period of this plus the steps that follow it in serve (the
// closing writes, about ten seconds, and SOLOCLEF_API_SHUTDOWN_GRACE_SECONDS),
// with room to spare; change both together.
const workDrain = 5 * time.Minute

// drainWork is the first part of a stop: the server goes on serving while the
// work in flight finishes, within workDrain.
//
// The listener stays open on purpose. Docker starts the new container only
// once this one has exited, so a server that stopped answering during the
// drain would take the whole site down for as long as the drain lasted. It
// answers as usual instead, with one difference the caller has already made
// by ending the signal context: the Studio runner claims no new job (the
// next process claims what is queued meanwhile, so a click during a deploy
// is made a little later, not refused). A chat message sent during the drain
// is answered by this process, and is held to the same bound.
func drainWork(logger *slog.Logger, bound time.Duration, works ...inflight.Work) {
	logger.Info("draining: waiting for the work in flight", slog.Duration("bound", bound))
	started := time.Now()
	waiting, cancel := context.WithTimeout(context.Background(), bound)
	defer cancel()
	if inflight.AllIdle(waiting, works...) {
		logger.Info("drained: nothing is in flight", slog.Duration("took", time.Since(started).Round(time.Millisecond)))
		return
	}
	logger.Warn("the drain's bound passed with work in flight; it is stopped now (Studio jobs are queued again for the next process)",
		slog.Duration("bound", bound))
}
