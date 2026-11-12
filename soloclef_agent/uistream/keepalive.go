package uistream

import (
	"context"
	"fmt"
	"time"
)

// KeepAlive writes a comment line whenever the stream has been silent for
// interval, until ctx ends or the stream does. It blocks, so run it in a
// goroutine for the life of the stream.
//
// A model can think, and a tool can run, for longer than a proxy between the
// server and the reader will hold an idle connection open. A comment line is
// ignored by every SSE reader and resets every such timer.
//
// It sleeps until the moment the stream will have been silent for interval,
// measured from the last frame, and not on a fixed tick. A fixed tick that
// fires just before the silence reaches interval would skip that turn and
// leave the stream silent for nearly twice as long, which is long enough for
// a proxy configured with twice the interval to cut the connection.
func (w *Writer) KeepAlive(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		return fmt.Errorf("uistream: keep-alive interval must be positive, got %s", interval)
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
		w.mu.Lock()
		over := w.err != nil || w.closed
		wait := interval
		if !over {
			if silent := w.cfg.Now().Sub(w.lastWrite); silent >= interval {
				w.buf = append(w.buf[:0], ": keepalive\n\n"...)
				w.flush()
			} else {
				wait = interval - silent
			}
		}
		w.mu.Unlock()
		if over {
			// The stream has ended or its reader has gone: there is nothing
			// left to keep alive.
			return nil
		}
		timer.Reset(wait)
	}
}
