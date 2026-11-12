package utf8text

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

func read(t *testing.T, content string, maxBytes int64) (string, error) {
	t.Helper()
	return Read(context.Background(), parse.StringSource("file.txt", content), maxBytes)
}

func TestPlainUTF8IsReturnedUnchanged(t *testing.T) {
	text, err := read(t, "Olá, 世界\nline two\n", 1024)
	if err != nil || text != "Olá, 世界\nline two\n" {
		t.Fatalf("text = %q, err = %v", text, err)
	}
}

func TestTheUTF8ByteOrderMarkIsNotPartOfTheText(t *testing.T) {
	text, err := read(t, "\xEF\xBB\xBFname,age\n", 1024)
	if err != nil || text != "name,age\n" {
		t.Fatalf("text = %q, err = %v", text, err)
	}
}

func TestUTF16IsDecodedWhenItsByteOrderMarkSaysSo(t *testing.T) {
	// "hé" in UTF-16, both byte orders, as Excel and Notepad write it.
	little := "\xFF\xFEh\x00\xE9\x00"
	big := "\xFE\xFF\x00h\x00\xE9"
	for name, content := range map[string]string{"little endian": little, "big endian": big} {
		text, err := read(t, content, 1024)
		if err != nil || text != "hé" {
			t.Errorf("%s: text = %q, err = %v", name, text, err)
		}
	}
}

func TestAFileInAnotherEncodingIsRefusedNotMangled(t *testing.T) {
	// "café" in Latin-1: 0xE9 alone is not UTF-8.
	_, err := read(t, "caf\xE9", 1024)
	if parse.KindOf(err) != parse.UnsupportedFormat || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("err = %v", err)
	}
}

func TestABinaryFileIsRefused(t *testing.T) {
	_, err := read(t, "PK\x03\x04\x00\x00", 1024)
	if parse.KindOf(err) != parse.UnsupportedFormat || !strings.Contains(err.Error(), "binary") {
		t.Fatalf("err = %v", err)
	}
}

func TestAFileOverTheLimitIsRefusedFromItsReportedSize(t *testing.T) {
	source := parse.StringSource("big.txt", "0123456789")
	source.Open = func(context.Context) (io.ReadCloser, error) {
		t.Fatal("the file was opened although its size already rules it out")
		return nil, nil
	}
	if _, err := Read(context.Background(), source, 9); parse.KindOf(err) != parse.TooLarge {
		t.Fatalf("err = %v", err)
	}
}

func TestAFileLongerThanItsReportedSizeIsStillCaught(t *testing.T) {
	source := parse.StringSource("big.txt", "0123456789")
	source.Size = 2
	if _, err := Read(context.Background(), source, 9); parse.KindOf(err) != parse.TooLarge {
		t.Fatalf("err = %v", err)
	}
}

func TestAFileExactlyAtTheLimitIsRead(t *testing.T) {
	if text, err := read(t, "0123456789", 10); err != nil || text != "0123456789" {
		t.Fatalf("text = %q, err = %v", text, err)
	}
}

func TestAStorageFailureIsTransient(t *testing.T) {
	broken := errors.New("bucket unreachable")
	source := parse.Source{Name: "a.txt", Size: 5, Open: func(context.Context) (io.ReadCloser, error) {
		return nil, broken
	}}
	_, err := Read(context.Background(), source, 100)
	if parse.KindOf(err) != parse.Unavailable || !errors.Is(err, broken) {
		t.Fatalf("err = %v", err)
	}
}

func TestTheLimitIsRequired(t *testing.T) {
	if _, err := read(t, "x", 0); err == nil {
		t.Fatal("a zero limit was accepted")
	}
}
