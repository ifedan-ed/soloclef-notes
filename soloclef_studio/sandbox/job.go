package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// The labels a job's sandbox carries. The account is under a label of its
// own, not accountLabel: the per-account limit is a limit on conversations'
// sandboxes, and a video being rendered must not take a slot a person's
// chat needs (or be refused because their chats hold them all). How many
// renders run at once is the worker's concurrency, which is the bound that
// matters for a host's CPU.
const (
	jobLabel        = "soloclef_job"
	jobAccountLabel = "soloclef_job_account"
)

// jobName is what a job's name may be: a label value the server accepts,
// and nothing a caller could use to name another job's label by accident.
var jobName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ForJob returns the sandbox of one job attempt, renewed, creating it if
// there is none. name identifies the attempt ("deliverable-job-7-attempt-2"):
// a new attempt is a new name, and so a fresh sandbox, while a worker that
// takes over an attempt whose worker died finds the same one (and is
// expected to start it over: see the caller).
//
// A job runs in one place at a time (its row is claimed), so there is no
// race to create two; the lock is held anyway, so that this process never
// does.
func (c *Client) ForJob(ctx context.Context, userID, name string) (*Session, error) {
	if !jobName.MatchString(name) {
		return nil, fmt.Errorf("sandbox: %q is not a job name (lower-case letters, digits and hyphens)", name)
	}
	key := "job:" + name
	lock := c.lockFor(key)
	lock.Lock()
	defer lock.Unlock()

	ctx, cancel := context.WithTimeout(ctx, c.settings.CommandTimeout)
	defer cancel()
	live, err := c.list(ctx, jobLabel, name)
	if err != nil {
		return nil, err
	}
	for _, found := range live {
		err := c.renew(ctx, found)
		if err == nil {
			c.remember(key, found)
			return &Session{client: c, ID: found, key: key}, nil
		}
		if !errors.Is(err, ErrGone) {
			return nil, err
		}
	}
	id, err := c.create(ctx, map[string]string{jobLabel: name, jobAccountLabel: strings.ToLower(userID)})
	if err != nil {
		return nil, err
	}
	c.remember(key, id)
	return &Session{client: c, ID: id, key: key}, nil
}

// TerminateJob removes a job attempt's sandbox, if it has one. It is called
// when the attempt ends, however it ended, and before a dead attempt is
// taken over; an attempt with no sandbox is not an error.
func (c *Client) TerminateJob(ctx context.Context, name string) error {
	if !jobName.MatchString(name) {
		return fmt.Errorf("sandbox: %q is not a job name", name)
	}
	return c.terminate(ctx, jobLabel, name, "job:"+name)
}

// OpenFile streams a file out of the sandbox, for one too large to hold in
// memory (a rendered video). The caller reads and closes it; the stream is
// bounded by ctx, not by CommandTimeout, because a large file takes as long
// as it takes. A missing file is ErrNotFound.
func (s *Session) OpenFile(ctx context.Context, path string) (io.ReadCloser, error) {
	response, err := s.client.call(ctx, http.MethodGet, s.execd("/files/download")+"?"+url.Values{"path": {path}}.Encode(), nil, "")
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusNotFound {
		drain(response)
		return nil, ErrNotFound
	}
	if response.StatusCode != http.StatusOK {
		return nil, s.gone(decode(response, "reading a file", nil))
	}
	return response.Body, nil
}
