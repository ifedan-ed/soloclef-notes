package jmapmail

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// mostUploadReplyBytes bounds the answer to an upload: a few ids and a
// size.
const mostUploadReplyBytes = 64 << 10

// TooLargeToUpload is the sentence for a file over the server's upload
// limit, for a caller that knows the size before it reads the file: the
// limit is the server's own (Session.MaxSizeUpload), so the person is told
// whose it is.
func TooLargeToUpload(name string, size, limit int64) *Failure {
	return &Failure{Message: fmt.Sprintf("%s is %s; this mail server accepts files of at most %s (its upload limit), so it cannot be attached",
		plainText(name, 120), ReadableSize(size), ReadableSize(limit))}
}

// ReadableSize is a size as a person reads it: "4.2 MB", or "12 KB" for
// one under a megabyte, which "0.0 MB" would hide.
func ReadableSize(size int64) string {
	if size < 1<<20 {
		return fmt.Sprintf("%d KB", max((size+1023)/1024, 1))
	}
	return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
}

// upload sends one file to the server's upload endpoint (RFC 8620 6.1) and
// returns the blob id a message can then name as an attachment. The file
// is streamed from body, exactly size bytes, never held whole in memory;
// a body that yields another number of bytes, or fails, fails the upload,
// and the half-sent blob is never named by anything (the server removes
// an unreferenced upload on its own).
//
// The conversation's deadline is the caller's: a file takes longer than a
// method call, so Send, its one caller here, gives the whole send the
// client's AttachmentTimeout when it carries files.
func (c Client) upload(ctx context.Context, session Session, credential Credential, body io.Reader, size int64, mediaType string) (string, error) {
	if session.UploadURL == "" {
		return "", &Failure{Message: "The mail server offers no upload of files, so this message cannot carry attachments"}
	}
	address := strings.NewReplacer("{accountId}", url.PathEscape(session.AccountID)).Replace(session.UploadURL)
	if got, err := Origin(address); err != nil || got != session.Origin {
		return "", &Failure{Message: "The mail server pointed to an address on another server, which is not followed"}
	}
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	status, _, raw, err := c.exchangeBody(ctx, session.Origin, credential, http.MethodPost, address,
		&requestBody{Reader: body, Type: mediaType, Length: size}, mostUploadReplyBytes)
	if err != nil {
		return "", err
	}
	if status == http.StatusRequestEntityTooLarge {
		return "", &Failure{Message: "The mail server refused a file as larger than it accepts"}
	}
	if status != http.StatusOK && status != http.StatusCreated {
		var problem struct {
			Type   string `json:"type"`
			Detail string `json:"detail"`
		}
		_ = json.Unmarshal(raw, &problem)
		if problem.Type != "" {
			return "", refusal(strings.TrimPrefix(problem.Type, "urn:ietf:params:jmap:error:"), problem.Detail)
		}
		return "", &Failure{Message: "The mail server did not accept a file"}
	}
	var uploaded struct {
		AccountID string `json:"accountId"`
		BlobID    string `json:"blobId"`
		Size      int64  `json:"size"`
	}
	if json.Unmarshal(raw, &uploaded) != nil || uploaded.BlobID == "" {
		return "", errBroken
	}
	// The server's own count of what it stored: a blob of another length
	// is not the file that was meant, whatever went wrong on the way.
	if uploaded.Size != size {
		return "", &Failure{Message: "The mail server stored a file of another size than was sent; nothing was sent"}
	}
	return uploaded.BlobID, nil
}
