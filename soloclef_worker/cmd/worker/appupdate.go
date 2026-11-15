package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_celery/routing"
	"git.danvics.com/danvics/soloclef/soloclef_notify/notify"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/appupdate"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/jobreconcile"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/jobs"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/pushdelivery"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/workerconfig"
)

// How the App Store update check behaves: decisions about the job, made
// here with their reasons, like the ones in jobs.go.
const (
	// The pass runs hourly, so an account held back by its quiet hours is
	// told within an hour of them ending; Apple is asked once a day, which
	// is as often as its lookup changes (it takes up to a day to show a
	// release anyway).
	appUpdateEvery      = time.Hour
	appStoreLookupEvery = 24 * time.Hour
	// Telling an account is one short transaction and a publish: a few
	// thousand a pass is minutes at most, and the rest are the next
	// pass's, so a release day spreads its pushes over a few hours instead
	// of waking every iPhone in the same minute.
	appUpdateAccounts = 5000
	appUpdateDeadline = 10 * time.Minute
	// Apple's lookup answers in well under a second; fifteen is a lookup
	// that is not going to answer, and the next pass asks again.
	appStoreTimeout = 15 * time.Second
)

// appUpdateJobs is the hourly App Store update check, or nothing on a
// deployment with phone pushes off or no App Store listing of its own (it
// says which at start). The job is also returned so the worker can seed its
// first tick.
func appUpdateJobs(settings workerconfig.Config, push pushdelivery.Config, pool *pgxpool.Pool, rdb redis.UniversalClient,
	notifier *notify.Notifier, log *slog.Logger) ([]jobs.Job, *appupdate.Job, error) {
	if !push.Enabled {
		// Telling a phone about an update is a push; with pushes off the
		// setting is not read at all (internal/appupdate, config.go).
		return nil, nil, nil
	}
	bundle, err := appupdate.LoadBundleID(os.Getenv)
	if err != nil {
		return nil, nil, err
	}
	if bundle == "" {
		log.Warn("no App Store bundle id is set (" + appupdate.BundleIDSetting + "): iPhones are not told when a new version is out")
		return nil, nil, nil
	}
	if settings.DefaultQueue == "" {
		return nil, nil, fmt.Errorf("%s is not set", workerconfig.DefaultQueue)
	}
	store := appupdate.AppStore{BaseURL: appupdate.LookupURL, BundleID: bundle, HTTP: &http.Client{Timeout: appStoreTimeout}}
	if err := store.Check(); err != nil {
		return nil, nil, err
	}
	replyTo, err := produce.NewID()
	if err != nil {
		return nil, nil, err
	}
	job := &appupdate.Job{
		BundleID: bundle,
		Store:    store,
		Rows:     appupdate.PoolRows{Pool: pool},
		Notifier: notifier,
		// Through the routing table every producer uses, so the tick goes
		// where the other ticks go.
		Publisher: produce.Producer{
			Broker: jobreconcile.RedisBroker{Redis: rdb}, Routes: routing.Table{Default: settings.DefaultQueue},
			Origin: produce.Origin(), ReplyTo: replyTo,
		},
		Fence:  appupdate.RedisFence{Redis: rdb},
		Timing: appupdate.Timing{Every: appUpdateEvery, LookupEvery: appStoreLookupEvery, Accounts: appUpdateAccounts},
		Log:    log, Now: time.Now,
	}
	if err := job.Check(); err != nil {
		return nil, nil, err
	}
	log.Info("iPhones on an older app are told when the App Store has a new version", "bundle", bundle)
	return []jobs.Job{{Task: appupdate.TickTask, Deadline: appUpdateDeadline, Run: job.Run}}, job, nil
}
