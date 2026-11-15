package maildrafts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"testing"
)

// fileOf is the attachment the draft holds for data.
func fileOf(data []byte) Attachment {
	digest := sha256.Sum256(data)
	return Attachment{Name: "report.pdf", Type: "application/pdf", Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:]),
		Source: AttachmentSource{Kind: SourceDocument, FileID: 1}}
}

func TestOnlyTheConfirmedBytesPassTheStream(t *testing.T) {
	confirmed := bytes.Repeat([]byte("the quarterly report "), 4000)
	want := fileOf(confirmed)

	// The same bytes pass whole, and the stream ends as streams end.
	stream := verified(io.NopCloser(bytes.NewReader(confirmed)), want)
	got, err := io.ReadAll(stream)
	if err != nil || !bytes.Equal(got, confirmed) || stream.Close() != nil || stream.wasChanged() {
		t.Fatalf("the confirmed file: %d bytes, %v", len(got), err)
	}

	for name, data := range map[string][]byte{
		"one byte changed": func() []byte { changed := bytes.Clone(confirmed); changed[100] ^= 1; return changed }(),
		"shorter":          confirmed[:len(confirmed)-1],
		"longer":           append(bytes.Clone(confirmed), '!'),
	} {
		stream := verified(io.NopCloser(bytes.NewReader(data)), want)
		_, err := io.ReadAll(stream)
		// The end of the stream is where the check is: a consumer sees an
		// error where io.EOF would be, so an upload never completes.
		if !errors.Is(err, errFileChanged) || !stream.wasChanged() || !errors.Is(stream.Close(), errFileChanged) {
			t.Errorf("%s: read %v, changed %v", name, err, stream.wasChanged())
		}
	}

	// A consumer that stops at the stated length (and never reads the end)
	// still gets the verdict: Close reads the end and gives it.
	changed := bytes.Clone(confirmed)
	changed[len(changed)-1] ^= 1
	stream = verified(io.NopCloser(bytes.NewReader(changed)), want)
	if _, err := io.ReadFull(stream, make([]byte, len(changed))); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); !errors.Is(err, errFileChanged) {
		t.Fatalf("stopped at the length, a changed file closes with %v", err)
	}
	stream = verified(io.NopCloser(bytes.NewReader(confirmed)), want)
	if _, err := io.ReadFull(stream, make([]byte, len(confirmed))); err != nil || stream.Close() != nil {
		t.Fatalf("stopped at the length, the confirmed file closes with an error: %v", err)
	}
}
