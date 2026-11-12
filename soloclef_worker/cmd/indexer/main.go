// Command indexer is the Go indexing worker: it consumes the queue
// soloclef.index and, for each index_document message, makes one stored
// document searchable (internal/indexing says how).
//
// It is a command of its own, beside cmd/worker, for two reasons. cmd/worker
// runs Python task bodies and so lives in the 5 GB backend image; this runs
// no Python and needs only its own binary and a tokenizer file. And this
// links the chunker's cgo library, which cmd/worker must not: it is built
// with CGO_ENABLED=0.
//
// Nothing is discovered and nothing is defaulted. The settings are listed in
// internal/indexconfig, soloclef_ingest/parserconfig and internal/broker; a
// missing one stops the process and is named. Before it takes a message it
// proves each thing it depends on: the database is at this build's schema
// version, the file store opens, the proxy embeds one text at the profile's
// dimension, and the Qdrant collection is the profile's shape.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
	"git.danvics.com/danvics/soloclef/soloclef_filestore"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parserconfig"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/vision"
	"git.danvics.com/danvics/soloclef/soloclef_schema/schemaversion"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/chunking"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/embedding"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/indexconfig"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/indexing"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/indexstore"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/indextask"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/metrics"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/namerequest"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/process"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/vectorindex"
)

// startupTimeout bounds the checks made before the first message. A
// dependency that does not answer in this long is down, and a process that
// says so is more use than one still waiting.
const startupTimeout = 60 * time.Second

// visionTimeout bounds one picture's description. A vision model answers
// in seconds; two minutes is the Python tier's limit, and one silent that
// long is not going to answer.
const visionTimeout = 2 * time.Minute

// ownModelConnectTimeout bounds the connect and TLS handshake to an owner's
// own model connection: the worker's value, for the same endpoints.
const ownModelConnectTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		slog.Error("the indexing worker stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// The queue first: the cheapest thing to get wrong, and the most
	// dangerous. This process acknowledges any message that is not
	// index_document, so pointed at a Python queue it would empty it.
	queue, err := broker.LoadConfig(os.Getenv)
	if err != nil {
		return err
	}
	if len(queue.Queues) != 1 || queue.Queues[0] != indextask.Queue {
		return fmt.Errorf("%s must be exactly %q: this worker runs %s and nothing else, and would discard any other queue's messages (it is %q)",
			broker.EnvQueues, indextask.Queue, indextask.Task, strings.Join(queue.Queues, ","))
	}
	queue.Name = process.WorkerName(os.Getenv)

	config, err := indexconfig.Load(os.Getenv)
	if err != nil {
		return err
	}
	profile, err := embedding.ProfileNamed(config.EmbeddingProfile)
	if err != nil {
		return fmt.Errorf("%s: %w", indexconfig.EmbeddingProfile, err)
	}
	parsers, err := parserconfig.Load(os.Getenv)
	if err != nil {
		return err
	}

	starting, started := context.WithTimeout(ctx, startupTimeout)
	defer started()

	rdb, err := process.OpenRedis(os.Getenv)
	if err != nil {
		return err
	}
	defer func() { _ = rdb.Close() }()
	if err := rdb.Ping(starting).Err(); err != nil {
		return fmt.Errorf("the broker is unreachable: %w", err)
	}

	pool, err := pgxpool.New(starting, config.DatabaseURL)
	if err != nil {
		return fmt.Errorf("%s: %w", indexconfig.DatabaseURL, err)
	}
	defer pool.Close()
	// The database must be at the schema version this binary was built
	// against, or the process stops here and says which way it differs. It
	// never migrates: `migrate up` is a deployment's own step
	// (soloclef_schema/schemaversion says why). This one check stands where
	// two narrower ones were: that the parser columns exist, and that the
	// Python tier's chunk outbox trigger is off. The migrations have the
	// columns and no trigger, so a database at this version has both.
	if err := schemaversion.Check(starting, pool); err != nil {
		return err
	}
	documents := indexstore.New(pool)

	files, err := filestore.OpenStores(config.FileStorage)
	if err != nil {
		return err
	}
	defer func() { _ = files.Close() }()

	chunker, err := chunking.Open(profile, config.TokenizerDirectory)
	if err != nil {
		return err
	}
	defer func() { _ = chunker.Close() }()

	embedder, err := embedding.New(profile, embedding.Config{
		BaseURL: config.ProxyURL, Key: config.ProxyKey,
		HTTP: &http.Client{Timeout: config.EmbeddingTimeout},
	})
	if err != nil {
		return err
	}
	if err := embedder.Probe(starting); err != nil {
		return fmt.Errorf("profile %s could not embed a test text through the proxy: %w", profile.Name, err)
	}

	vectors, err := vectorindex.New(vectorindex.Config{
		URL: config.QdrantURL, APIKey: config.QdrantKey,
		Alias: config.QdrantAlias, Collection: config.QdrantCollection,
		Dimension: profile.Dimension,
		HTTP:      &http.Client{Timeout: config.QdrantTimeout},
	})
	if err != nil {
		return err
	}
	if err := vectors.Ensure(starting); err != nil {
		return err
	}

	log := slog.Default().With("worker", queue.Name)
	// The vision model is the owner's, and only for a document whose
	// upload asked for it: a proxy name, reached through the same proxy as
	// the embedding model, or a model of the owner's own connection, called
	// at its own address through the egress guard with the key the API
	// sealed under this deployment's secret. No probe: a deployment with
	// vision unused must not fail to start for it.
	proxyVision := vision.Client{BaseURL: config.ProxyURL, Key: config.ProxyKey, HTTP: &http.Client{}, Timeout: visionTimeout}
	// The retry policy is the chat's, which Connections requires; the
	// indexer opens no chat model, and a description that fails for a
	// reason that may pass is retried by handing the document back.
	own, err := ownmodels.ForDeployment(pool, config.SecretKey, ownModelConnectTimeout,
		model.RetryPolicy{Attempts: 1, Backoff: time.Second, MaxBackoff: time.Second})
	if err != nil {
		return err
	}
	resolver := ownmodels.VisionResolver{Proxy: proxyVision, Own: own}
	if err := resolver.Check(); err != nil {
		return err
	}
	indexer, err := indexing.New(documents, files, parsers, chunker, embedder, vectors,
		indexing.Config{MaxAttempts: config.MaxAttempts, RetryDelay: config.RetryDelay,
			Vision: indexing.ResolvedVision{Resolver: resolver}, VisionModel: config.VisionModel}, log)
	if err != nil {
		return err
	}
	naming, err := namerequest.New(pool, rdb, config.DefaultQueue, log)
	if err != nil {
		return err
	}
	jobs := indexing.NewStatistics()
	runner, err := indextask.New(indexer, jobs.Observe, naming)
	if err != nil {
		return err
	}
	consumer, err := broker.NewConsumer(rdb, queue, runner)
	if err != nil {
		return err
	}

	services := []process.Service{{Name: "consumer", Run: consumer.Run}}
	if address := metrics.LoadAddr(os.Getenv); address != "" {
		server, err := metrics.NewServer(address, metrics.Consumer(consumer), indexMetrics(jobs))
		if err != nil {
			return err
		}
		slog.Info("serving metrics", "address", server.Address(), "path", metrics.MetricsPath)
		services = append(services, process.Service{Name: "metrics", Run: server.Run})
	}

	slog.Info("the indexing worker is starting",
		"worker", queue.Name, "queue", indextask.Queue,
		"queue_concurrency", fmt.Sprint(queue.QueueConcurrency),
		"embedding_profile", profile.Name, "model", profile.Model, "dimension", profile.Dimension,
		"chunk_size", profile.ChunkSize, "sizer", string(profile.Sizer),
		"default_parser", parsers.Default().String(), "vision_model", config.VisionModel,
		"qdrant_alias", config.QdrantAlias, "qdrant_collection", config.QdrantCollection,
		"file_storage", files.Configured(),
		"max_attempts", config.MaxAttempts, "retry_delay", config.RetryDelay)

	err = process.Together(ctx, services...)
	if errors.Is(err, broker.ErrShutdownIncomplete) {
		// Documents were cut off mid-index. Nothing is lost: each is left
		// "processing" and its message becomes visible again after the
		// visibility window, so it is an error to notice, not to repair.
		return err
	}
	if err != nil {
		return err
	}
	slog.Info("the indexing worker stopped with nothing in flight")
	return nil
}
