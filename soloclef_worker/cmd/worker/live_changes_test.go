package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_notify/livechange"
	"git.danvics.com/danvics/soloclef/soloclef_notify/livechange/livechangetest"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/drivesync"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/notebookname"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/podcast"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/videojob"
)

// Each fake answers the one write a case calls; the embedded interface is
// nil, so a call the case did not expect panics rather than passes.
type podcastRows struct {
	podcast.Rows
	err error
}

func (r podcastRows) Fail(context.Context, int64, string, string) error { return r.err }
func (r podcastRows) Deliver(context.Context, podcast.Delivery) (int64, bool, error) {
	return 40, r.err == nil, r.err
}

type videoRows struct {
	videojob.Rows
	ours bool
}

func (r videoRows) Heartbeat(context.Context, videojob.Claimed, string, int) (bool, error) {
	return r.ours, nil
}
func (r videoRows) Claim(context.Context, int64, string, videojob.ClaimRules) (videojob.Claim, error) {
	return videojob.Claim{}, nil
}

type namingRows struct {
	notebookname.Rows
	renamed bool
}

func (r namingRows) Rename(context.Context, notebookname.Notebook, notebookname.Naming) (bool, error) {
	return r.renamed, nil
}

type driveRows struct {
	drivesync.Rows
	requeued bool
}

func (r driveRows) Requeue(context.Context, string, int64) (bool, error) { return r.requeued, nil }
func (r driveRows) Synced(context.Context, string, int64) error          { return nil }

func TestAWriteThatChangedARowIsToldAndOneThatDidNotIsNot(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		write func(changes livechange.Publisher)
		want  []livechange.Kind
	}{
		{"a podcast failed", func(c livechange.Publisher) {
			_ = livePodcasts{Rows: podcastRows{}, changes: c}.Fail(ctx, 1, "u1", "no voice")
		}, []livechange.Kind{livechange.Outputs}},
		{"a podcast's failure not written", func(c livechange.Publisher) {
			_ = livePodcasts{Rows: podcastRows{err: errors.New("down")}, changes: c}.Fail(ctx, 1, "u1", "no voice")
		}, nil},
		{"a podcast delivered, with its transcript document", func(c livechange.Publisher) {
			_, _, _ = livePodcasts{Rows: podcastRows{}, changes: c}.Deliver(ctx, podcast.Delivery{Run: podcast.Run{UserID: "u1"}})
		}, []livechange.Kind{livechange.Outputs, livechange.Documents}},
		{"a video moved to its next phase", func(c livechange.Publisher) {
			_, _ = liveVideos{Rows: videoRows{ours: true}, changes: c}.Heartbeat(ctx, videojob.Claimed{UserID: "u1"}, "narrating", 40)
		}, []livechange.Kind{livechange.Outputs}},
		{"a video attempt no longer its own", func(c livechange.Publisher) {
			_, _ = liveVideos{Rows: videoRows{ours: false}, changes: c}.Heartbeat(ctx, videojob.Claimed{UserID: "u1"}, "narrating", 40)
		}, nil},
		{"a claim that found nothing", func(c livechange.Publisher) {
			_, _ = liveVideos{Rows: videoRows{}, changes: c}.Claim(ctx, 1, "t", videojob.ClaimRules{})
		}, nil},
		{"a notebook named itself", func(c livechange.Publisher) {
			_, _ = liveNaming{Rows: namingRows{renamed: true}, changes: c}.Rename(ctx, notebookname.Notebook{ID: 5, UserID: "u1"}, notebookname.Naming{})
		}, []livechange.Kind{livechange.Notebooks, livechange.Outputs, livechange.Documents}},
		{"a notebook already named", func(c livechange.Publisher) {
			_, _ = liveNaming{Rows: namingRows{}, changes: c}.Rename(ctx, notebookname.Notebook{ID: 5, UserID: "u1"}, notebookname.Naming{})
		}, nil},
		{"a Drive document queued again", func(c livechange.Publisher) {
			_, _ = liveDrive{Rows: driveRows{requeued: true}, changes: c}.Requeue(ctx, "u1", 9)
		}, []livechange.Kind{livechange.Documents}},
		{"a Drive document not queued", func(c livechange.Publisher) {
			_, _ = liveDrive{Rows: driveRows{}, changes: c}.Requeue(ctx, "u1", 9)
		}, nil},
		{"a Drive connection synced", func(c livechange.Publisher) {
			_ = liveDrive{Rows: driveRows{}, changes: c}.Synced(ctx, "u1", 3)
		}, []livechange.Kind{livechange.Connectors}},
	}
	for _, c := range cases {
		recorder := &livechangetest.Recorder{}
		c.write(recorder)
		if got := recorder.Kinds("u1"); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: told %v, want %v", c.name, got, c.want)
		}
	}
}
