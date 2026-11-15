package jmapmail

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// Download is an attachment's bytes. One larger than most is a *Failure
// saying so, read no further than one byte past the limit; a caller checks
// the attachment's stated Size first and never asks for one it knows is
// too large. The address is the session's download template filled in
// (RFC 8620 6.2), on the server's own origin. It has the deadline of a
// conversation that moves a file (AttachmentTimeout).
func (c Client) Download(ctx context.Context, session Session, credential Credential, attachment Attachment, most int64) ([]byte, error) {
	ctx, cancel := c.boundedForFiles(ctx)
	defer cancel()
	if session.DownloadURL == "" || attachment.BlobID == "" {
		return nil, &Failure{Message: "The mail server offers no download of this attachment"}
	}
	name := attachment.Name
	if name == "" {
		name = "attachment"
	}
	kind := attachment.Type
	if kind == "" {
		kind = "application/octet-stream"
	}
	address := strings.NewReplacer(
		"{accountId}", url.PathEscape(session.AccountID),
		"{blobId}", url.PathEscape(attachment.BlobID),
		"{name}", url.PathEscape(name),
		"{type}", url.QueryEscape(kind),
	).Replace(session.DownloadURL)
	// The filled-in values are escaped, so they cannot move the address to
	// another host; checked all the same, as every address is.
	if got, err := Origin(address); err != nil || got != session.Origin {
		return nil, &Failure{Message: "The mail server pointed to an address on another server, which is not followed"}
	}
	status, _, body, err := c.exchange(ctx, session.Origin, credential, http.MethodGet, address, nil, most)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, &Failure{Message: "The mail server did not hand over this attachment"}
	}
	return body, nil
}
