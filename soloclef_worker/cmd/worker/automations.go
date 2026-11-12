package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint/postgres"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/openaicompat"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/deliverables"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/mailsignin"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/voices"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_celery/routing"
	filestore "git.danvics.com/danvics/soloclef/soloclef_filestore"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/parserconfig"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/automationrun"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/automationtriggers"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/indexrequest"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/jobs"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/workerconfig"
)

// How automations behave, as opposed to where things are: decisions about
// the jobs, made once here with the reason.
const (
	// A run may be given a day by its own definition; the message's
	// deadline is that with room to write the ending.
	automationRunDeadline = 24*time.Hour + 10*time.Minute
	// A tick is a few statements and a few publishes.
	automationTickDeadline = time.Minute

	// A schedule fires within a minute of its moment: cron's own grain.
	automationTickEvery = time.Minute
	// Two hundred triggers and two hundred waiting runs a tick is far more
	// than a minute brings; whatever is left is the next tick's.
	automationTickBatch = 200
	// A run still unclaimed after two minutes has probably lost its
	// message; published again, a duplicate is acknowledged with nothing
	// done.
	automationRepublishAfter = 2 * time.Minute
	// A run five minutes past its own limit was not ended by its worker,
	// which ends it at the limit: the worker is gone.
	automationOverdueGrace = 5 * time.Minute

	// A cancel from the page stops the spending within moments.
	automationStatusInterval = 2 * time.Second
	// A failed step waits five seconds, then (exponential) ten, twenty...
	automationRetryStep = 5 * time.Second
	// Twenty rounds of refused approvals is a model that will not stop
	// asking, not a task making progress.
	automationMaxPauses = 20
	// A tool result is kept in the run's row up to twenty thousand
	// characters: enough to see what a search found, not a whole document
	// per call in a row that the page loads at once.
	automationResultChars = 20_000

	// As the chat's: eight tool calls of one batch at once, and an answer
	// cut off by the output cap is carried on twice at most.
	automationParallelTools = 8
	automationContinuations = 2
	// The proxy's model list, as the chat reads it.
	automationModelListFresh   = 30 * time.Second
	automationModelListTimeout = 10 * time.Second

	// The event stream is read five seconds at a time, so a stopping
	// worker is not held by a read; an entry its reader never acknowledged
	// is taken over after a minute.
	automationEventBlock     = 5 * time.Second
	automationEventClaimIdle = time.Minute
	automationEventBatch     = 50
)

// automationParts is what a worker that runs automations also starts and
// stops, besides its jobs.
type automationParts struct {
	tick   *automationtriggers.Tick
	events *automationtriggers.Events
	close  func()
}

// automationJobs builds the two automation jobs: the run and the schedule
// tick. On a deployment that switched automations off it is the one job
// that ends each run with a sentence saying so, and no parts.
func automationJobs(settings workerconfig.Config, pool *pgxpool.Pool, rdb redis.UniversalClient, writer filestore.Writer,
	indexer *indexrequest.Publisher, log *slog.Logger) ([]jobs.Job, *automationParts, error) {
	cfg, err := automationrun.LoadConfig(os.Getenv)
	if err != nil {
		return nil, nil, err
	}
	if !cfg.Enabled {
		return []jobs.Job{{Task: automationrun.Task, Deadline: refusalDeadline, Run: automationrun.SwitchedOff{Pool: pool}.Run}}, nil, nil
	}
	if settings.DefaultQueue == "" {
		return nil, nil, fmt.Errorf("%s is not set", workerconfig.DefaultQueue)
	}
	// No whole-request timeout: an answer streams for as long as it takes,
	// and each call is bounded by its run's context.
	httpClient := &http.Client{}
	open := func(name string) (model.Model, error) {
		client, err := openaicompat.New(openaicompat.Config{BaseURL: settings.ProxyURL, APIKey: settings.ProxyKey, Model: name, HTTPClient: httpClient})
		if err != nil {
			return nil, err
		}
		return model.WithRetry(client, model.RetryPolicy{Attempts: modelAttempts, Backoff: modelBackoff, MaxBackoff: modelMaxBackoff})
	}
	if _, err := open(settings.ChatModel); err != nil {
		return nil, nil, err
	}
	rows := automationrun.Store{Pool: pool}
	catalogue := &assembly.Catalogue{
		URL: settings.ProxyURL, Key: settings.ProxyKey, HTTP: httpClient,
		Timeout: automationModelListTimeout, Fresh: automationModelListFresh, Now: time.Now,
		// The one name on the proxy this tier knows is not a chat model.
		NotChat: []string{cfg.EmbeddingModel},
	}
	if err := catalogue.Check(); err != nil {
		return nil, nil, err
	}
	// The model that answers a run is the model that answers the account's
	// chat: its own choice from the proxy's list, else the deployment's.
	// Or a model of the account's own OpenAI-compatible connection, called
	// at the person's address through the egress guard
	// (soloclef_assistant/ownmodels).
	own, err := ownmodels.ForDeployment(pool, settings.SecretKey, ownModelConnectTimeout,
		model.RetryPolicy{Attempts: modelAttempts, Backoff: modelBackoff, MaxBackoff: modelMaxBackoff})
	if err != nil {
		return nil, nil, err
	}
	models := &assembly.Models{Catalogue: catalogue, Choices: rows, Default: settings.ChatModel, Open: open, Own: own}

	checkpoints, err := postgres.New(pool)
	if err != nil {
		return nil, nil, err
	}
	// The table the loop saves to, made if it is not there: the API tier
	// makes it as it starts, and a worker may start first.
	starting, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := checkpoints.EnsureSchema(starting); err != nil {
		return nil, nil, fmt.Errorf("the agent's checkpoint table: %w", err)
	}
	// The parsers a run's mail and Drive tools read a fetched file with:
	// the block the API and the indexer are given (x-parsers), so a file
	// reads the same in a run as in a chat and as an upload.
	parsers, err := parserconfig.Load(os.Getenv)
	if err != nil {
		return nil, nil, err
	}
	tools, closeTools, err := assembly.NewTools(assembly.ToolSettings{
		ModelURL: settings.ProxyURL, ModelKey: settings.ProxyKey,
		EmbeddingModel: cfg.EmbeddingModel, EmbeddingDimension: cfg.EmbeddingDimension,
		QdrantURL: settings.QdrantURL, QdrantAPIKey: settings.QdrantKey, QdrantCollection: cfg.QdrantCollection,
		QdrantHNSWEf: cfg.QdrantHNSWEf, QdrantQuantized: cfg.QdrantQuantized,
		QdrantRescore: cfg.QdrantRescore, QdrantOversampling: cfg.QdrantOversampling,
		NCBITool: cfg.NCBITool, NCBIEmail: cfg.NCBIEmail, NCBIAPIKey: cfg.NCBIAPIKey,
		ValkeyURL: cfg.AppRedisURL, Brand: cfg.Brand,
		// A run reads its owner's mail and Drive with the chat's own tools:
		// the deployment's secret key opens the credentials the API sealed
		// (automationrun.SecretKeyFile, read and checked with the rest of
		// the automation settings), and each kind is renewed through the
		// registration the API connected it with.
		SecretKey:     cfg.SecretKey,
		MicrosoftMail: microsoftMail(cfg),
		GoogleDrive:   assembly.GoogleDriveSettings{ClientID: cfg.GoogleClientID, ClientSecret: cfg.GoogleClientSecret},
		// The API's Nextcloud: a run reads and writes its owner's Nextcloud
		// files with the chat's own tools; none offers a run no such tools.
		NextcloudURL: cfg.NextcloudURL,
		// A fetched file is read by the parser its connection chose, and a
		// picture described by the account's vision model, else this one.
		Parsers: parsers, VisionModel: cfg.VisionModel,
		// The name this tier offers beyond the assembly's own, so that a
		// connected app's tool of the same name is given another.
		ReservedNames: []string{deliverables.DelegateName},
	}, assembly.ToolDeps{Pool: pool, HTTP: httpClient, Logger: log, Own: own})
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) ([]jobs.Job, *automationParts, error) {
		closeTools()
		return nil, nil, err
	}
	replyTo, err := produce.NewID()
	if err != nil {
		return fail(err)
	}
	publisher := produce.Producer{
		Broker: automationtriggers.RedisBroker{Redis: rdb}, Routes: routing.Table{Default: settings.DefaultQueue},
		Origin: produce.Origin(), ReplyTo: replyTo,
	}

	// The speech setting as it was written, <roster>/<model>: the voices
	// catalogue reads it by the API tier's own rules.
	speechService := strings.TrimSpace(os.Getenv(workerconfig.SpeechService))
	// The artifact specialist, built the way the API builds the chat's
	// (deliverables.NewSpecialist), so a run makes what a chat makes: each
	// optional part is the configured one or the type that says this
	// deployment has none. A podcast's voices are proposed from the speech
	// service the podcast job itself speaks with.
	voiceSettings := voices.Settings{Service: speechService, APIBase: settings.ProxyURL, APIKey: settings.ProxyKey, PodcastVoices: cfg.PodcastVoices}
	catalogueOfVoices, err := voices.New(voiceSettings, voices.NewProxy(voiceSettings, httpClient), log)
	if err != nil {
		return fail(err)
	}
	specialist, _, err := deliverables.NewSpecialist(deliverables.Settings{
		ModelURL: settings.ProxyURL, ModelKey: settings.ProxyKey,
		ImageModel: cfg.ImageModel, VisionModel: cfg.VisionModel,
		SandboxEnabled: cfg.SandboxEnabled, Sandbox: cfg.Sandbox,
	}, deliverables.Parts{
		Pool: pool, Store: checkpoints, Files: writer, HTTP: httpClient, Voices: catalogueOfVoices, Logger: log,
		// An account's own vision model, for reading an infographic back.
		Own: own,
		// A video a run asks for is rendered by this tier's own video job,
		// from the message every tier publishes for one.
		PublishVideo: func(ctx context.Context, jobID int64, taskID string) error {
			_, err := publisher.Send(ctx, deliverables.VideoMessage(jobID, taskID))
			return err
		},
		Index: func(ctx context.Context, documentID int64, _ string) error { return indexer.Index(ctx, documentID) },
	})
	if err != nil {
		return fail(err)
	}

	// The chat's assembly with this tier's rows. Memory is left out on
	// purpose; internal/automationrun says why.
	agents := assembly.Assembly{
		Models: models, Store: checkpoints, Rows: rows,
		Documents: tools.Documents, Web: tools.Web, Academic: tools.Academic, Pages: tools.PageTool, Connected: tools.Connected,
		Mail: tools.Mail, Drive: tools.Drive,
		Artifacts: deliverables.PerTurn{Specialist: specialist},
		Limits:    assembly.Limits{MaxSteps: cfg.MaxSteps, MaxParallelTools: automationParallelTools, Continuations: automationContinuations},
		Now:       time.Now, Brand: cfg.Brand,
	}
	if err := agents.Check(); err != nil {
		return fail(err)
	}

	run := &automationrun.Job{
		Rows: rows, Assembler: agents, Store: checkpoints, Publisher: publisher, Indexer: indexer,
		Timing: automationrun.Timing{
			StatusInterval: automationStatusInterval, RetryStep: automationRetryStep,
			MaxPauses: automationMaxPauses, ResultChars: automationResultChars,
		},
		Log: log, Now: time.Now,
	}
	if err := run.Check(); err != nil {
		return fail(err)
	}
	starter := automationtriggers.Starter{Rows: automationtriggers.Store{Pool: pool, Log: log}, Publisher: publisher, Log: log}
	tick := &automationtriggers.Tick{
		Starter: starter, Fence: automationtriggers.RedisFence{Redis: rdb},
		Timing: automationtriggers.Timing{
			Every: automationTickEvery, Batch: automationTickBatch,
			RepublishAfter: automationRepublishAfter, OverdueGrace: automationOverdueGrace,
		},
		Now: time.Now,
	}
	if err := tick.Check(); err != nil {
		return fail(err)
	}
	// The stream is on the API tier's Redis, which need not be the broker.
	appRedis, err := redis.ParseURL(cfg.AppRedisURL)
	if err != nil {
		return fail(fmt.Errorf("%s: %w", automationrun.AppRedisURL, err))
	}
	// A blocking read holds its connection for Block; the read timeout
	// must outlast it or every idle read would be reported as a failure.
	appRedis.ReadTimeout = automationEventBlock + 5*time.Second
	stream := redis.NewClient(appRedis)
	name, err := os.Hostname()
	if err != nil {
		_ = stream.Close()
		return fail(err)
	}
	events := &automationtriggers.Events{
		Redis: stream, Starter: starter, Consumer: name,
		Block: automationEventBlock, ClaimIdle: automationEventClaimIdle, Batch: automationEventBatch, Now: time.Now,
	}
	if err := events.Check(); err != nil {
		_ = stream.Close()
		return fail(err)
	}
	return []jobs.Job{
			{Task: automationrun.Task, Deadline: automationRunDeadline, Run: run.Run},
			{Task: automationtriggers.Task, Deadline: automationTickDeadline, Run: tick.Run},
		}, &automationParts{tick: tick, events: events, close: func() {
			_ = stream.Close()
			closeTools()
		}}, nil
}

// microsoftMail is where a Microsoft mailbox's token is refreshed, or
// nothing on a deployment that has not set Microsoft mail up.
func microsoftMail(cfg automationrun.Config) assembly.MicrosoftMailSettings {
	if cfg.MicrosoftMailClientID == "" {
		return assembly.MicrosoftMailSettings{}
	}
	return assembly.MicrosoftMailSettings{
		ClientID: cfg.MicrosoftMailClientID, ClientSecret: cfg.MicrosoftMailClientSecret,
		TokenURL: mailsignin.TokenURL(cfg.MicrosoftMailTenant),
	}
}

// start seeds the schedule tick and starts reading events, for a worker
// that consumes the queue the tick is on. The returned function stops the
// reader, waits for it, and releases what the parts opened.
func (p *automationParts) start(ctx context.Context, seed bool) (func(), error) {
	if seed {
		// On the reconciler's terms: a tick already on its way is not
		// published twice, and a worker that cannot publish does not start.
		if err := p.tick.Seed(ctx); err != nil {
			p.close()
			return nil, fmt.Errorf("starting the automation schedule: %w", err)
		}
	}
	// A context of the reader's own, so that stopping does not depend on
	// why the process is stopping: a worker that ends on an error has not
	// necessarily had ctx cancelled.
	readerCtx, stopReading := context.WithCancel(ctx)
	var reading sync.WaitGroup
	reading.Add(1)
	go func() {
		defer reading.Done()
		p.events.Run(readerCtx)
	}()
	return func() {
		stopReading()
		reading.Wait()
		p.close()
	}, nil
}
