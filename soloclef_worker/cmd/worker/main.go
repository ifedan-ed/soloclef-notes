// Command worker is the Go worker tier: it consumes the Celery queues it is
// given, runs each job's body as Python, writes the rows Python's AsyncResult
// reads, keeps the schedule Celery Beat kept, and serves what it knows about
// itself to Prometheus. Nothing about it is discovered — the queues, the
// limits, the command that runs a body and whether this process schedules at
// all are configuration, and a worker given none of it consumes nothing and
// schedules nothing.
//
// Restarting it is not a drain. In-flight work is given ShutdownGrace and then
// killed, which on the heavy queue means a billing hold held until the stale
// sweep and a row left GENERATING, so every restart goes behind the deploy
// gate first:
//
//	python3 soloclef_backend/scripts/deploy_gate.py --wait
//
// See custom/docs/go-worker.md.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/beat"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/executor"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/metrics"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/results"
	"github.com/redis/go-redis/v9"
)

func main() {
	if err := run(); err != nil {
		slog.Error("the worker stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	rdb, err := openRedis(os.Getenv)
	if err != nil {
		return err
	}
	defer func() { _ = rdb.Close() }()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("the broker is unreachable: %w", err)
	}

	var (
		services   []service
		collectors []metrics.Collector
	)

	consumer, bodies, err := consumption(ctx, rdb)
	if err != nil {
		return err
	}
	if bodies != nil {
		defer bodies.Close()
	}
	if consumer != nil {
		services = append(services, service{name: "consumer", run: consumer.Run})
		collectors = append(collectors, metrics.Consumer(consumer))
	}

	scheduler, err := scheduling(rdb)
	if err != nil {
		return err
	}
	if scheduler != nil {
		services = append(services, service{name: "beat", run: scheduler.Run})
		collectors = append(collectors, metrics.Beat(scheduler))
	}

	// A process that neither consumes nor schedules is a process that was
	// started with no configuration, which is the safe default made loud
	// rather than a worker idling.
	if len(services) == 0 {
		return broker.ErrNoQueues
	}

	if addr := metrics.LoadAddr(os.Getenv); addr != "" {
		server, err := metrics.NewServer(addr, collectors...)
		if err != nil {
			return err
		}
		slog.Info("serving metrics", "address", server.Address(), "path", metrics.MetricsPath)
		services = append(services, service{name: "metrics", run: server.Run})
	}

	err = together(ctx, services...)
	if errors.Is(err, broker.ErrShutdownIncomplete) {
		// The grace period ended with work still running, which means jobs
		// were killed mid-flight: whatever they held is still held. This is
		// what the deploy gate exists to prevent, so it is an error, not a
		// clean stop.
		return err
	}
	if err != nil {
		return err
	}
	slog.Info("the worker stopped with nothing in flight")
	return nil
}

// consumption builds the consumer and the warm task processes, or nothing at
// all when this process was given no queues.
func consumption(ctx context.Context, rdb redis.UniversalClient) (*broker.Consumer, *executor.Executor, error) {
	cfg, err := broker.LoadConfig(os.Getenv)
	if err != nil {
		return nil, nil, err
	}
	if len(cfg.Queues) == 0 {
		return nil, nil, nil
	}
	cfg.Name = workerName(os.Getenv)

	tasks, err := executor.LoadConfig(os.Getenv)
	if err != nil {
		return nil, nil, err
	}
	tasks.Worker = cfg.Name

	rows, err := results.NewBackend(rdb, results.CeleryResultExpiry)
	if err != nil {
		return nil, nil, err
	}
	bodies, err := executor.New(tasks, rows)
	if err != nil {
		return nil, nil, err
	}
	consumer, err := broker.NewConsumer(rdb, cfg, bodies)
	if err != nil {
		bodies.Close()
		return nil, nil, err
	}

	slog.Info("the worker is starting",
		"worker", cfg.Name,
		"queues", strings.Join(cfg.Queues, ","),
		"queue_concurrency", fmt.Sprint(cfg.QueueConcurrency),
		"task_concurrency", fmt.Sprint(cfg.TaskConcurrency),
		"task_command", strings.Join(tasks.Command, " "),
		"pool_size", tasks.PoolSize,
		"shutdown_grace", cfg.ShutdownGrace)

	// Before the first message rather than after it: the imports are paid at
	// startup, so whoever enqueues first does not pay them. A worker that
	// cannot start a task process is a worker that would hand every message
	// back, so it does not start consuming at all.
	if err := bodies.Warm(ctx); err != nil {
		bodies.Close()
		return nil, nil, fmt.Errorf("the task processes could not be readied: %w", err)
	}
	return consumer, bodies, nil
}

// scheduling builds the beat, or nothing when this process was not asked to
// schedule — which is the default, because two schedulers run every scheduled
// task twice.
func scheduling(rdb redis.UniversalClient) (*beat.Beat, error) {
	if !beat.Enabled(os.Getenv) {
		return nil, nil
	}
	cfg, err := beat.LoadConfig(os.Getenv)
	if err != nil {
		return nil, err
	}
	scheduler, err := beat.New(rdb, cfg)
	if err != nil {
		return nil, err
	}
	slog.Info("this worker will also schedule",
		"beat", cfg.Name,
		"entries", len(cfg.Entries),
		"lease", cfg.KeyPrefix+":lease",
		"lease_ttl", cfg.LeaseTTL,
		"poll_interval", cfg.PollInterval)
	return scheduler, nil
}

// workerName is what every result row says ran the job, as Celery's hostname
// does. Unset it is this container's host, which is unique per replica and
// needs no configuration.
func workerName(getenv func(string) string) string {
	if given := strings.TrimSpace(getenv(broker.EnvName)); given != "" {
		return given
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return "go-worker@" + host
}
