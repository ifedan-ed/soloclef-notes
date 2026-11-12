package main

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_celery/routing"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/drivegrant"
	"git.danvics.com/danvics/soloclef/soloclef_filestore"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/drivesync"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/indexrequest"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/jobreconcile"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/jobs"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/workerconfig"
)

// How the Drive sync behaves: decisions about the job, made here with
// their reasons, like the ones in jobs.go.
const (
	// One sync is at most fifty files of at most the upload limit each,
	// read a few at a time. Half an hour is several times what that takes
	// on a slow line, and short enough that a sync stuck on a file that
	// never ends gives its slot back.
	driveSyncDeadline = 30 * time.Minute
	// Connections are looked at every five minutes, so a sync that is due
	// starts within five minutes of its time: nothing a daily sync
	// notices. Two hundred a tick is far more than fall due in five
	// minutes on a deployment of any size this stack is run at, and
	// whatever is left is the next tick's.
	driveTickEvery    = 5 * time.Minute
	driveTickBatch    = 200
	driveTickDeadline = time.Minute
	// Google answers a token request or a metadata read in well under a
	// second; ten seconds is a Google that is not answering. A download's
	// first byte can take longer for an export Google has to render.
	driveTokenTimeout  = 10 * time.Second
	driveHeaderTimeout = 2 * time.Minute
)

// driveJobs is the Google Drive sync and the tick that schedules it, or,
// on a deployment that switched the sync off, the one job that tells each
// picked file so at once. The tick is nil when there is none to seed.
func driveJobs(settings workerconfig.Config, pool *pgxpool.Pool, rdb redis.UniversalClient, writer filestore.Writer,
	indexer *indexrequest.Publisher, log *slog.Logger) ([]jobs.Job, *drivesync.Tick, error) {
	config, err := drivesync.LoadConfig(os.Getenv)
	if err != nil {
		return nil, nil, err
	}
	rows := drivesync.Store{Pool: pool}
	if !config.Enabled {
		return []jobs.Job{{Task: drivesync.Task, Deadline: refusalDeadline, Run: drivesync.Refusal{Rows: rows, Log: log}.Run}}, nil, nil
	}
	renewals := drivesync.NewHTTP(driveTokenTimeout)
	renewals.Timeout = driveTokenTimeout
	sync := &drivesync.Job{
		Rows: rows,
		Google: drivegrant.Client{
			ClientID: config.ClientID, ClientSecret: config.ClientSecret,
			TokenURL: drivesync.GoogleTokenURL, HTTP: renewals,
		},
		Files: writer, Indexer: indexer,
		SecretKey: config.SecretKey, DriveURL: drivesync.GoogleDriveURL, HTTP: drivesync.NewHTTP(driveHeaderTimeout),
		MaxFileBytes: config.MaxFileBytes, Concurrency: config.Concurrency,
		Log: log, Now: time.Now,
	}
	if err := sync.Check(); err != nil {
		return nil, nil, err
	}
	if settings.DefaultQueue == "" {
		return nil, nil, fmt.Errorf("%s is not set", workerconfig.DefaultQueue)
	}
	replyTo, err := produce.NewID()
	if err != nil {
		return nil, nil, err
	}
	tick := &drivesync.Tick{
		Schedule: drivesync.DueStore{Pool: pool},
		// Through the routing table every producer uses, so the syncs it
		// publishes go where the API's own go.
		Publisher: produce.Producer{
			Broker: jobreconcile.RedisBroker{Redis: rdb}, Routes: routing.Table{Default: settings.DefaultQueue},
			Origin: produce.Origin(), ReplyTo: replyTo,
		},
		Fence: drivesync.RedisFence{Redis: rdb},
		Every: driveTickEvery, Batch: driveTickBatch, Log: log, Now: time.Now,
	}
	if err := tick.Check(); err != nil {
		return nil, nil, err
	}
	return []jobs.Job{
		{Task: drivesync.Task, Deadline: driveSyncDeadline, Run: sync.Run},
		{Task: drivesync.TickTask, Deadline: driveTickDeadline, Run: tick.Run},
	}, tick, nil
}
