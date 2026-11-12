// Command worker is the Go worker tier: it consumes the background queues
// it is given and runs each job's body here, as Go (internal/jobs says
// which), keeps the schedule if it is asked to, and serves what it knows
// about itself to Prometheus. Nothing about it is discovered — the queues,
// the limits, every address and model, and whether this process schedules
// at all are configuration, and a worker given no queues consumes nothing.
//
// It runs no Python. Every job body that used to be a Python process (the
// podcast, the notebook naming, the folder deletion, the account erasure)
// is Go now, keyed by the account that owns the rows, and so is the video
// (internal/videojob). The beat schedules nothing; the two periodic pieces
// of work, republishing queued jobs whose message was lost
// (internal/jobreconcile) and syncing the Google Drive connections that
// are due (internal/drivesync), are each a message that schedules its own
// successor.
//
// A restart is not a drain. In-flight work is given ShutdownGrace; a
// podcast cut off by it goes back to its queue and carries on from the
// lines already voiced.
package main

import (
	"context"
	"errors"
	"fmt"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/automationtriggers"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"github.com/redis/go-redis/v9"

	"git.danvics.com/danvics/soloclef/soloclef_celery/routing"
	"git.danvics.com/danvics/soloclef/soloclef_filestore"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/beat"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/drivesync"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/jobreconcile"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/metrics"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/process"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/workerconfig"
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

	rdb, err := process.OpenRedis(os.Getenv)
	if err != nil {
		return err
	}
	defer func() { _ = rdb.Close() }()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("the broker is unreachable: %w", err)
	}

	var (
		services   []process.Service
		collectors []metrics.Collector
	)

	consumer, closeJobs, err := consumption(ctx, rdb)
	if err != nil {
		return err
	}
	defer closeJobs()
	if consumer != nil {
		services = append(services, process.Service{Name: "consumer", Run: consumer.Run})
		collectors = append(collectors, metrics.Consumer(consumer))
	}

	scheduler, err := scheduling(rdb)
	if err != nil {
		return err
	}
	if scheduler != nil {
		services = append(services, process.Service{Name: "beat", Run: scheduler.Run})
		collectors = append(collectors, metrics.Beat(scheduler))
	}

	// A process that neither consumes nor schedules was started with no
	// configuration: the safe default, made loud rather than idling.
	if len(services) == 0 {
		return broker.ErrNoQueues
	}

	if addr := metrics.LoadAddr(os.Getenv); addr != "" {
		server, err := metrics.NewServer(addr, collectors...)
		if err != nil {
			return err
		}
		slog.Info("serving metrics", "address", server.Address(), "path", metrics.MetricsPath)
		services = append(services, process.Service{Name: "metrics", Run: server.Run})
	}

	err = process.Together(ctx, services...)
	if errors.Is(err, broker.ErrShutdownIncomplete) {
		// The grace period ended with work still running. Each job's
		// message keeps its claim and becomes visible again after the
		// visibility window, so nothing is lost; it is still an error,
		// because the work was cut off.
		return err
	}
	if err != nil {
		return err
	}
	slog.Info("the worker stopped with nothing in flight")
	return nil
}

// consumption builds the consumer and every job, or nothing at all when
// this process was given no queues. The returned function closes what the
// jobs hold open.
func consumption(ctx context.Context, rdb redis.UniversalClient) (*broker.Consumer, func(), error) {
	nothing := func() {}
	cfg, err := broker.LoadConfig(os.Getenv)
	if err != nil {
		return nil, nothing, err
	}
	if len(cfg.Queues) == 0 {
		return nil, nothing, nil
	}
	cfg.Name = process.WorkerName(os.Getenv)

	settings, err := workerconfig.Load(os.Getenv)
	if err != nil {
		return nil, nothing, err
	}
	// The database before a queue is consumed: the jobs read and write it,
	// so one at another schema version is refused here instead.
	pool, err := openDatabase(ctx, os.Getenv)
	if err != nil {
		return nil, nothing, err
	}
	files, err := filestore.OpenStores(settings.FileStorage)
	if err != nil {
		pool.Close()
		return nil, nothing, err
	}
	closeAll := func() {
		_ = files.Close()
		pool.Close()
	}
	log := slog.Default().With("worker", cfg.Name)
	runner, err := table(settings, pool, rdb, files, log)
	if err != nil {
		closeAll()
		return nil, nothing, err
	}
	consumer, err := broker.NewConsumer(rdb, cfg, runner)
	if err != nil {
		closeAll()
		return nil, nothing, err
	}
	// The reconciler's first tick, from a worker that will take it: one
	// that consumes only the heavy queue leaves the seeding to one that
	// consumes the queue the tick is on. A tick already on its way is not
	// published twice (internal/jobreconcile), and a seed that fails stops
	// the start: a worker that could not publish cannot be relied on to
	// repair a publish either.
	if slices.Contains(cfg.Queues, routing.Table{Default: settings.DefaultQueue}.QueueFor(jobreconcile.Task)) {
		if err := runner.reconciler.Seed(ctx); err != nil {
			closeAll()
			return nil, nothing, fmt.Errorf("starting the queued-job reconciler: %w", err)
		}
	}
	// The Drive schedule's first tick, on the same terms.
	if runner.driveTick != nil && slices.Contains(cfg.Queues, routing.Table{Default: settings.DefaultQueue}.QueueFor(drivesync.TickTask)) {
		if err := runner.driveTick.Seed(ctx); err != nil {
			closeAll()
			return nil, nothing, fmt.Errorf("starting the Google Drive schedule: %w", err)
		}
	}
	// The automation schedule's first tick, on the same terms, and the
	// reader of the event stream, which stops with ctx. What they opened is
	// released after the reader has returned.
	if runner.automations != nil {
		seed := slices.Contains(cfg.Queues, routing.Table{Default: settings.DefaultQueue}.QueueFor(automationtriggers.Task))
		stopAutomations, err := runner.automations.start(ctx, seed)
		if err != nil {
			closeAll()
			return nil, nothing, err
		}
		closeJobs := closeAll
		closeAll = func() {
			stopAutomations()
			closeJobs()
		}
	}
	slog.Info("the worker is starting",
		"worker", cfg.Name,
		"queues", strings.Join(cfg.Queues, ","),
		"jobs", strings.Join(runner.Tasks(), ","),
		"queue_concurrency", fmt.Sprint(cfg.QueueConcurrency),
		"task_concurrency", fmt.Sprint(cfg.TaskConcurrency),
		"chat_model", settings.ChatModel, "quick_model", settings.QuickModel,
		"speech", settings.Speech.Model, "file_storage", files.Configured(),
		"shutdown_grace", cfg.ShutdownGrace)
	return consumer, closeAll, nil
}

// scheduling builds the beat, or nothing when this process was not asked
// to schedule — which is the default, because two schedulers run every
// scheduled task twice.
func scheduling(rdb redis.UniversalClient) (*beat.Beat, error) {
	if !beat.Enabled(os.Getenv) {
		return nil, nil
	}
	cfg, err := beat.LoadConfig(os.Getenv)
	if err != nil {
		return nil, err
	}
	scheduler, err := beat.New(rdb, cfg)
	if errors.Is(err, beat.ErrNoSchedule) {
		return nil, fmt.Errorf("%s is on, and the Go stack has nothing to schedule (internal/beat/schedule.go says why each Python entry is gone): set it off: %w", beat.EnvEnabled, err)
	}
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
