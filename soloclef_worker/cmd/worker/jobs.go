package main

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_celery/routing"
	"git.danvics.com/danvics/soloclef/soloclef_filestore"
	"git.danvics.com/danvics/soloclef/soloclef_studio/sandbox"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/accounterasure"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/audiofile"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/documentremoval"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/drivesync"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/folderdeletion"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/indexrequest"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/jobreconcile"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/jobs"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/llm"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/notebookname"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/podcast"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/speech"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/videojob"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/workerconfig"
)

// How the jobs behave, as opposed to where things are. These are decisions
// about the jobs, made once here with the reason, not deployment settings.
const (
	// A model call that failed for a reason that may pass (a 429, a 5xx, a
	// dropped connection) is tried three times in all, two seconds apart
	// and then four: long enough for a rate limit to clear, short enough
	// that a draft is not held for minutes by a provider that is down.
	modelAttempts   = 3
	modelBackoff    = 2 * time.Second
	modelMaxBackoff = 30 * time.Second

	// A person's own model endpoint gets ten seconds to accept a
	// connection; its answer then streams for as long as it is long.
	ownModelConnectTimeout = 10 * time.Second

	// The wait before a line's second attempt; it grows by the same step.
	speechBackoff = 2 * time.Second

	// A podcast asks whether it was cancelled at most this often: often
	// enough that a cancel stops the spending within moments, rarely enough
	// that a long render is not a stream of queries.
	podcastStatusInterval = 5 * time.Second

	// A video asks whether it was cancelled every two seconds, so a cancel
	// stops a render within moments, and says its worker lives every
	// minute. Ten silent minutes is ten missed keep-alives: the worker is
	// gone (a restart kills an attempt without a word), not rendering
	// slowly, and a redelivered message takes the attempt over, twice at
	// most, so an attempt whose worker dies on every render ends.
	videoStatusInterval = 2 * time.Second
	videoKeepAlive      = time.Minute
	videoStaleAfter     = 10 * time.Minute
	videoTakeovers      = 2
	// The scenes may be repaired twice, across the build, the layout
	// check of their stills and the MP4's checks together.
	videoRepairs = 2

	// A removal that a store did not answer is tried three times, five and
	// then ten seconds apart, before the job gives up and leaves the folder
	// or the account for the person to delete again.
	removalAttempts = 3
	removalBackoff  = 5 * time.Second

	// A queued job whose message was never published is looked for once a
	// minute and published again once it has sat untouched for two: long
	// enough that a job a worker is about to take is not published twice
	// as a matter of course, short enough that a person whose retry met a
	// broker that was down waits minutes, not until they notice. A hundred
	// jobs a tick is more than a broker outage of any length queues here,
	// and whatever is left is the next tick's.
	reconcileEvery      = time.Minute
	reconcileStaleAfter = 2 * time.Minute
	reconcileBatch      = 100
)

// The longest each job may run. A podcast's is the Celery limit the Python
// task ran under: an hour-long episode is hundreds of lines, and each of
// its calls is bounded on its own as well.
const (
	podcastDeadline = broker.CeleryHardTimeLimit
	namingDeadline  = 5 * time.Minute
	folderDeadline  = time.Hour
	erasureDeadline = 2 * time.Hour
	refusalDeadline = time.Minute
	// A tick is one statement and a few publishes.
	reconcileDeadline = time.Minute
	// A ten-minute video with its repairs took the Python worker over an
	// hour end to end; its Celery limit was two hours, kept here.
	videoDeadline = 2 * time.Hour
)

// proxyHTTP is the client every proxy call is made with. The whole of a
// call is bounded by its own context; this bounds getting connected and
// getting the first byte of an answer, which a hung provider never sends.
func proxyHTTP() *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		ResponseHeaderTimeout: 5 * time.Minute,
		// A podcast voices up to SOLOCLEF_WORKER_TTS_CONCURRENCY lines at
		// once, each on its own connection to the proxy; keeping them open
		// between lines saves a handshake per line.
		MaxIdleConnsPerHost: 32,
		IdleConnTimeout:     90 * time.Second,
	}}
}

// built is the job table, and the one job the worker also starts itself.
type built struct {
	*jobs.Table
	// reconciler is the queued-job reconciler, whose first tick a worker
	// publishes as it starts (consumption).
	reconciler *jobreconcile.Job
	// driveTick is the Google Drive schedule's tick, seeded the same way;
	// nil on a deployment that does not sync Drive.
	driveTick *drivesync.Tick
	// automations is the schedule tick and the event reader a worker that
	// runs automations also starts; nil on a deployment that switched
	// automations off.
	automations *automationParts
}

// reconciler builds the job that republishes queued jobs whose message was
// lost. It publishes through the routing table every producer uses, from
// the deployment's default queue.
func reconciler(settings workerconfig.Config, pool *pgxpool.Pool, rdb redis.UniversalClient, log *slog.Logger) (*jobreconcile.Job, error) {
	if settings.DefaultQueue == "" {
		return nil, fmt.Errorf("%s is not set", workerconfig.DefaultQueue)
	}
	replyTo, err := produce.NewID()
	if err != nil {
		return nil, err
	}
	job := &jobreconcile.Job{
		Rows: jobreconcile.Store{Pool: pool},
		Publisher: produce.Producer{
			Broker: jobreconcile.RedisBroker{Redis: rdb}, Routes: routing.Table{Default: settings.DefaultQueue},
			Origin: produce.Origin(), ReplyTo: replyTo,
		},
		Fence:  jobreconcile.RedisFence{Redis: rdb},
		Timing: jobreconcile.Timing{Every: reconcileEvery, StaleAfter: reconcileStaleAfter, Batch: reconcileBatch},
		Log:    log, Now: time.Now,
	}
	return job, job.Check()
}

// table builds every job this worker runs, each checked before the first
// message is taken.
func table(settings workerconfig.Config, pool *pgxpool.Pool, rdb redis.UniversalClient, files *filestore.Stores, log *slog.Logger) (*built, error) {
	tools, err := audiofile.Find()
	if err != nil {
		return nil, err
	}
	writer, err := files.Writer()
	if err != nil {
		return nil, err
	}
	httpClient := proxyHTTP()
	proxy := llm.Proxy{
		BaseURL: settings.ProxyURL, Key: settings.ProxyKey, HTTP: httpClient, CallTimeout: settings.CallTimeout,
		Retry: model.RetryPolicy{Attempts: modelAttempts, Backoff: modelBackoff, MaxBackoff: modelMaxBackoff},
	}
	if err := proxy.Check(); err != nil {
		return nil, err
	}
	quick, err := proxy.Client(settings.QuickModel, true)
	if err != nil {
		return nil, err
	}
	// The model an account's long work is drafted by: its studio choice
	// through the proxy, or a model of its own OpenAI-compatible
	// connection called at the person's address through the egress guard
	// (soloclef_assistant/ownmodels), whose key this deployment's secret
	// opens.
	own, err := ownmodels.ForDeployment(pool, settings.SecretKey, ownModelConnectTimeout, proxy.Retry)
	if err != nil {
		return nil, err
	}
	writers := llm.Writers{Choices: llm.Choices{Pool: pool, Default: settings.ChatModel}, Proxy: proxy, Own: own}
	if err := writers.Check(); err != nil {
		return nil, err
	}
	indexer, err := indexrequest.New(rdb)
	if err != nil {
		return nil, err
	}
	voicer := speech.Client{
		BaseURL: settings.ProxyURL, Key: settings.ProxyKey, Service: settings.Speech, HTTP: httpClient, Audio: tools,
		AttemptTimeout: settings.SpeechAttemptTimeout, MaxAttempts: settings.SpeechMaxAttempts, Backoff: speechBackoff, Log: log,
	}
	if err := voicer.Check(); err != nil {
		return nil, err
	}
	vectors := documentremoval.Qdrant{URL: settings.QdrantURL, APIKey: settings.QdrantKey, Alias: settings.QdrantAlias,
		HTTP: &http.Client{Timeout: settings.QdrantTimeout}}
	if err := vectors.Check(); err != nil {
		return nil, err
	}
	removal := documentremoval.Removal{Pool: pool, Vectors: vectors, Files: files}

	podcasts := &podcast.Job{
		Rows:    podcast.Store{Pool: pool, Files: writer},
		Models:  writers,
		Voicer:  voicer,
		Audio:   tools,
		Indexer: indexer,
		Config: podcast.Config{
			CallTimeout: settings.CallTimeout, Speech: settings.Speech, Instructions: settings.SpeechInstructions,
			Concurrency: settings.SpeechConcurrency, WorkDirectory: settings.PodcastWorkDirectory,
			StatusInterval: podcastStatusInterval,
		},
		Log: log,
	}
	if err := podcasts.Check(); err != nil {
		return nil, err
	}
	naming := &notebookname.Job{Rows: notebookname.Store{Pool: pool}, Model: quick, CallTimeout: settings.CallTimeout, Log: log, Now: time.Now}
	if err := naming.Check(); err != nil {
		return nil, err
	}

	video, err := videoJob(settings, pool, writer, voicer, tools, indexer, writers, log)
	if err != nil {
		return nil, err
	}

	reconcile, err := reconciler(settings, pool, rdb, log)
	if err != nil {
		return nil, err
	}

	drive, driveTick, err := driveJobs(settings, pool, rdb, writer, indexer, log)
	if err != nil {
		return nil, err
	}

	automation, automations, err := automationJobs(settings, pool, rdb, writer, indexer, log)
	if err != nil {
		return nil, err
	}

	registered, err := jobs.New(log, append([]jobs.Job{
		jobs.Job{Task: podcast.Task, Deadline: podcastDeadline, Run: podcasts.Run},
		jobs.Job{Task: notebookname.Task, Deadline: namingDeadline, Run: naming.Run},
		jobs.Job{Task: folderdeletion.Task, Deadline: folderDeadline,
			Run: (&folderdeletion.Job{Pool: pool, Remover: removal, Attempts: removalAttempts, Backoff: removalBackoff}).Run},
		jobs.Job{Task: accounterasure.Task, Deadline: erasureDeadline,
			Run: (&accounterasure.Job{Eraser: removal, Attempts: removalAttempts, Backoff: removalBackoff}).Run},
		video,
		jobs.Job{Task: jobreconcile.Task, Deadline: reconcileDeadline, Run: reconcile.Run},
	}, append(drive, automation...)...)...)
	if err != nil {
		if automations != nil {
			automations.close()
		}
		return nil, fmt.Errorf("the job table: %w", err)
	}
	return &built{Table: registered, reconciler: reconcile, driveTick: driveTick, automations: automations}, nil
}

// videoJob is the video render, or, on a deployment that switched it off,
// the job that tells each queued video so at once.
func videoJob(settings workerconfig.Config, pool *pgxpool.Pool, writer filestore.Writer, voicer speech.Client,
	tools audiofile.Tools, indexer *indexrequest.Publisher, writers llm.Writers, log *slog.Logger) (jobs.Job, error) {
	video, err := videojob.LoadConfig(os.Getenv, settings.Speech)
	if err != nil {
		return jobs.Job{}, err
	}
	if !video.Enabled {
		return jobs.Job{Task: videojob.Task, Deadline: refusalDeadline, Run: videojob.Refusal{Pool: pool}.Run}, nil
	}
	// The sandbox client bounds every call by its context; a render's
	// stream is read for as long as the render runs.
	boxes, err := sandbox.New(video.Sandbox, &http.Client{})
	if err != nil {
		return jobs.Job{}, err
	}
	job := &videojob.Job{
		Rows:      videojob.Store{Pool: pool},
		Sandboxes: videojob.SandboxClient{Client: boxes},
		Models:    writers,
		Voicer:    voicer,
		Audio:     tools,
		Files:     writer,
		Indexer:   indexer,
		Config:    video,
		Timing: videojob.Timing{
			CallTimeout: settings.CallTimeout, StatusInterval: videoStatusInterval, KeepAlive: videoKeepAlive,
			Claim:        videojob.ClaimRules{StaleAfter: videoStaleAfter, MaxTakeovers: videoTakeovers},
			RepairCycles: videoRepairs, NarrationConcurrency: settings.SpeechConcurrency,
		},
		Log: log,
	}
	if err := job.Check(); err != nil {
		return jobs.Job{}, err
	}
	return jobs.Job{Task: videojob.Task, Deadline: videoDeadline, Run: job.Run}, nil
}
