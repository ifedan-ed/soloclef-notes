package drivesync

import (
	"context"
	"log/slog"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/drivesyncjob"
)

// reasonSwitchedOff is what a picked file's document carries on a
// deployment whose worker does not sync Drive.
const reasonSwitchedOff = "Google Drive import is switched off on this server."

// Refusal is the job a deployment with the sync switched off runs in its
// place. Such a deployment's API should not be offering Drive at all; if a
// message arrives all the same, the documents waiting for its files are
// told at once that nothing is coming, instead of showing a spinner that
// nothing will ever stop.
type Refusal struct {
	Rows Rows
	Log  *slog.Logger
}

// Run is the jobs.Handler.
func (r Refusal) Run(ctx context.Context, msg *celery.Message) error {
	request, err := drivesyncjob.Read(msg.Kwargs)
	if err != nil {
		return err
	}
	one{job: &Job{Rows: r.Rows, Log: r.Log}, userID: request.UserID, connectorID: request.ConnectorID}.
		abandon(ctx, request.FileIDs, reasonSwitchedOff)
	return nil
}
