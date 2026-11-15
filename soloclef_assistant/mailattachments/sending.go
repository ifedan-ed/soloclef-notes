package mailattachments

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/maildrafts"
)

// Open streams one of a draft's files, as the owner, for its send. A file
// whose row is gone is an error; the bytes are checked by the guard as
// they are read (maildrafts.verified), not here.
func (f *Files) Open(ctx context.Context, ownerID string, attachment maildrafts.Attachment) (io.ReadCloser, error) {
	if attachment.Source.Kind == maildrafts.SourceMail {
		data, err := f.download(ctx, ownerID, attachment)
		if err != nil {
			return nil, err
		}
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	row, err := f.row(ctx, ownerID, attachment.Source)
	if err != nil {
		return nil, err
	}
	store, err := f.Stores.Named(row.backend)
	if err != nil {
		return nil, err
	}
	return store.Open(ctx, row.key)
}

// Changed is the first of the files whose row no longer describes the file
// the draft was made with: removed, or of another size or digest. It reads
// rows only, so a card is drawn without reading any file. A forwarded
// attachment is not looked at: its blob cannot change, and a message
// deleted since fails the send in the server's own words.
func (f *Files) Changed(ctx context.Context, ownerID string, attachments []maildrafts.Attachment) (string, error) {
	for _, attachment := range attachments {
		if attachment.Source.Kind == maildrafts.SourceMail {
			continue
		}
		row, err := f.row(ctx, ownerID, attachment.Source)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return attachment.Name, nil
		case err != nil:
			return "", err
		}
		if row.size != attachment.Size || (row.checksum != nil && isDigest(*row.checksum) && !strings.EqualFold(*row.checksum, attachment.SHA256)) {
			return attachment.Name, nil
		}
	}
	return "", nil
}
