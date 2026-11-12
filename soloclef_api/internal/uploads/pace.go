package uploads

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// errTooLarge is what a capped file's reader returns once the file has
// passed the limit.
var errTooLarge = errors.New("uploads: the file is larger than the limit")

// pacedBody is the request body under a read deadline that moves forward
// while bytes keep arriving.
//
// cmd/api gives its server one ReadTimeout, which net/http turns into a
// single deadline for the whole request. That is right for a JSON body and
// wrong for an upload: a large file on a slow link takes longer than any
// limit that is sensible for everything else. Raising the server's limit
// would let a client that sends nothing hold a connection for as long.
//
// So the deadline is moved before a read: "the next bytes must arrive
// within the stall timeout". A client that keeps sending, however slowly,
// never meets it; one that stops is cut off after one stall timeout. It is
// internal/rollingdeadline's rule, applied to the read side.
type pacedBody struct {
	body    io.Reader
	control *http.ResponseController
	stall   time.Duration
	moved   time.Time
	// failed is the first error the body returned other than io.EOF: the
	// client's side failing. The route asks for it to tell an interrupted
	// upload (the client's fault) from a store that could not write.
	failed error
}

// moveEvery is how often the deadline is moved at most. Moving it is a
// system call; a read is 32 KB, so moving it on every read of a fast upload
// would be thousands of calls a second for no gain in accuracy.
const moveEvery = time.Second

// pace wraps the body and moves the deadline for the first time. It fails
// if the deadline cannot be moved, which means some wrapper between the
// route and the connection hides it; that is reported, not worked around,
// because reading under the server's fixed timeout would silently cut every
// upload longer than it.
func pace(body io.Reader, response http.ResponseWriter, stall time.Duration) (*pacedBody, error) {
	paced := &pacedBody{body: body, control: http.NewResponseController(response), stall: stall}
	if err := paced.move(time.Now()); err != nil {
		return nil, fmt.Errorf("uploads: the request's read deadline cannot be moved: %w", err)
	}
	return paced, nil
}

func (p *pacedBody) move(now time.Time) error {
	if err := p.control.SetReadDeadline(now.Add(p.stall)); err != nil {
		return err
	}
	p.moved = now
	return nil
}

func (p *pacedBody) Read(buffer []byte) (int, error) {
	if now := time.Now(); now.Sub(p.moved) >= moveEvery {
		if err := p.move(now); err != nil {
			p.failed = err
			return 0, err
		}
	}
	read, err := p.body.Read(buffer)
	if err != nil && err != io.EOF && p.failed == nil {
		p.failed = err
	}
	return read, err
}

// answerWithin gives the response a write deadline of its own. The server's
// write timeout was set when the request's headers were read, so after a
// long upload it is already in the past and the answer would never be sent.
func (p *pacedBody) answerWithin() error {
	return p.control.SetWriteDeadline(time.Now().Add(p.stall))
}

// capped reads at most limit bytes of a file and then fails with
// errTooLarge. It reads one byte past the limit to know the difference
// between a file of exactly the limit and a larger one.
type capped struct {
	file  io.Reader
	limit int64
	read  int64
}

func (c *capped) Read(buffer []byte) (int, error) {
	if c.read > c.limit {
		return 0, errTooLarge
	}
	if room := c.limit + 1 - c.read; int64(len(buffer)) > room {
		buffer = buffer[:room]
	}
	read, err := c.file.Read(buffer)
	c.read += int64(read)
	if c.read > c.limit {
		// The bytes read so far are not handed on: the store discards a
		// write whose body fails, so nothing of an oversized file is kept.
		return 0, errTooLarge
	}
	return read, err
}
