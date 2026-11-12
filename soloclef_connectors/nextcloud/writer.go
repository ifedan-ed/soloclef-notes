package nextcloud

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"
)

// MaxNameBytes bounds a file name a write makes: Nextcloud's own limit on
// a name is 250 bytes, and a name past it is refused there with a
// message nobody could act on.
const MaxNameBytes = 250

// Writer makes the two changes the chat may make to a person's files, each
// one that person approved: a new file, and a file moved to the bin. It is
// a type apart from Client so that the import, which nobody approves
// request by request, cannot write at all.
//
// A write is retried only on the answers that say the server did nothing
// (429, its rate and brute-force protection, and 503, maintenance): a
// gateway's 502 or 504 can come after the server did the write, and
// writing again would then be refused as "already there", which would be
// a false report of the first write's failure.
type Writer struct {
	client *Client
}

// NewWriter builds a writer for one account over the caller's transport,
// with the read client's timings and its refusal to follow a redirect.
func NewWriter(account Account, transport http.RoundTripper, timing Timing) *Writer {
	return &Writer{client: NewClient(account, transport, timing)}
}

// Client is the writer's own reader, for the reads around a write.
func (w *Writer) Client() *Client { return w.client }

// writeLater is the "later" answers a write is retried on.
func writeLater(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable
}

// ValidName is a file or folder name a write may use: one segment, not
// empty, not "." or "..", no slash or backslash or control character, at
// most MaxNameBytes, and valid UTF-8. Nothing is repaired: a name that
// needs repairing is not the one the person approved.
func ValidName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > MaxNameBytes || !utf8.ValidString(name) ||
		strings.TrimSpace(name) != name {
		return false
	}
	for _, r := range name {
		if r == '/' || r == '\\' || r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// Create writes a new file, and never replaces one: the request carries
// If-None-Match: *, so Nextcloud itself refuses when a file of that name
// is there (ErrExists), with no gap between a check and the write. A
// folder that is not there is ErrNoFolder; folders are not made, because
// a folder the model invented is not one the person picked a place in.
// The file's entry is read back, for its id and link.
func (w *Writer) Create(ctx context.Context, folder, name, contentType string, content []byte) (Entry, error) {
	folder, err := NormalizePath(folder)
	if err != nil {
		return Entry{}, err
	}
	if !ValidName(name) {
		return Entry{}, fmt.Errorf("%w: %q is not a file name", ErrInvalidPath, name)
	}
	path := strings.TrimRight(folder, "/") + "/" + name
	ctx, done := context.WithTimeout(ctx, w.client.timing.Listing)
	defer done()
	response, err := w.client.exchange(ctx, exchange{
		method: http.MethodPut, address: davURL(w.client.account.BaseURL, w.client.account.Username, path), named: path,
		header: http.Header{"Content-Type": {contentType}, "If-None-Match": {"*"}},
		body:   string(content),
		// 201 for a new file; 204 is what a replace answers, which the
		// precondition makes impossible, and is accepted only so that a
		// server that ignores it is not reported as failing a write it did.
		succeeded: func(status int) bool { return status == http.StatusCreated || status == http.StatusNoContent },
		later:     writeLater,
		refused: func(status int) error {
			switch status {
			case http.StatusPreconditionFailed:
				return ErrExists
			case http.StatusConflict:
				// WebDAV's answer to a PUT whose parent collection is not
				// there (RFC 4918, 9.7.1).
				return fmt.Errorf("%w: %s", ErrNoFolder, folder)
			}
			return nil
		},
	})
	if err != nil {
		return Entry{}, err
	}
	drain(response)
	return w.client.Stat(ctx, path)
}

// Trash deletes a file, which Nextcloud moves to the account's deleted
// files, where the person can restore it (on a server that keeps them,
// which is the trash bin app's default). The root is refused here: no
// approval of "move a file to the bin" covers every file.
func (w *Writer) Trash(ctx context.Context, path string) error {
	path, err := NormalizePath(path)
	if err != nil {
		return err
	}
	if path == Root {
		return fmt.Errorf("%w: the root of the user's files", ErrInvalidPath)
	}
	ctx, done := context.WithTimeout(ctx, w.client.timing.Listing)
	defer done()
	response, err := w.client.exchange(ctx, exchange{
		method: http.MethodDelete, address: davURL(w.client.account.BaseURL, w.client.account.Username, path), named: path,
		succeeded: func(status int) bool { return status == http.StatusNoContent || status == http.StatusOK },
		later:     writeLater,
	})
	if err != nil {
		return err
	}
	drain(response)
	return nil
}
