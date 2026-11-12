package e2eseed

import (
	"context"
	"net/http"
	"time"
)

// A share link expires at a time its owner chose, days ahead. A test of
// the expired page cannot wait days, so these two move a link's expiry to
// a second ago; everything that then refuses the link is the product's.

func (r Routes) expireSnapshot(x *exchange) {
	r.expire(x, "snapshot_id", "no such public link", func(ctx context.Context, id int64, at time.Time) (int64, error) {
		return New(r.Pool).ExpireSnapshotLink(ctx, ExpireSnapshotLinkParams{ID: id, ExpiredAt: at})
	})
}

func (r Routes) expireNotebookLink(x *exchange) {
	r.expire(x, "link_id", "no such notebook link", func(ctx context.Context, id int64, at time.Time) (int64, error) {
		return New(r.Pool).ExpireNotebookLink(ctx, ExpireNotebookLinkParams{ID: id, ExpiredAt: at})
	})
}

func (r Routes) expire(x *exchange, parameter, absent string, update func(context.Context, int64, time.Time) (int64, error)) {
	id, ok := x.id(parameter)
	if !ok {
		return
	}
	changed, err := update(x.ctx.Context(), id, r.now().Add(-time.Second))
	if err != nil {
		x.fail("could not expire the link", err)
		return
	}
	if changed == 0 {
		x.refuse(http.StatusNotFound, absent)
		return
	}
	x.send(http.StatusOK, map[string]int64{"expired": id})
}
