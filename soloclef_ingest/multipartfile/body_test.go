package multipartfile

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

func TestTheBodyIsWhatAStandardReaderExpects(t *testing.T) {
	source := parse.StringSource(`quarter "3" report.pdf`, "%PDF-1.7 the bytes")
	body, err := New(context.Background(), []Field{{"mode", "balanced"}, {"paginate", "true"}}, "file", source)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(body.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(raw)) != body.Length {
		t.Fatalf("Length = %d, body is %d bytes", body.Length, len(raw))
	}

	_, params, err := mime.ParseMediaType(body.ContentType)
	if err != nil {
		t.Fatal(err)
	}
	form, err := multipart.NewReader(bytes.NewReader(raw), params["boundary"]).ReadForm(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	if form.Value["mode"][0] != "balanced" || form.Value["paginate"][0] != "true" {
		t.Errorf("fields = %v", form.Value)
	}
	file := form.File["file"][0]
	if file.Filename != `quarter "3" report.pdf` {
		t.Errorf("filename = %q", file.Filename)
	}
	if got := file.Header.Get("Content-Type"); got != "application/pdf" {
		t.Errorf("content type = %q", got)
	}
	opened, _ := file.Open()
	content, _ := io.ReadAll(opened)
	if string(content) != "%PDF-1.7 the bytes" {
		t.Errorf("content = %q", content)
	}
}

type closeRecorder struct {
	io.Reader
	closed bool
}

func (c *closeRecorder) Close() error { c.closed = true; return nil }

func TestClosingTheBodyClosesTheFile(t *testing.T) {
	file := &closeRecorder{Reader: bytes.NewReader([]byte("x"))}
	source := parse.Source{Name: "a.pdf", Size: 1, Open: func(context.Context) (io.ReadCloser, error) { return file, nil }}

	body, err := New(context.Background(), nil, "file", source)
	if err != nil {
		t.Fatal(err)
	}
	body.Reader.Close()
	if !file.closed {
		t.Fatal("the file was left open")
	}
}

func TestAFileThatCannotBeOpenedIsReported(t *testing.T) {
	broken := errors.New("no such key")
	source := parse.Source{Name: "a.pdf", Size: 1, Open: func(context.Context) (io.ReadCloser, error) { return nil, broken }}
	if _, err := New(context.Background(), nil, "file", source); !errors.Is(err, broken) {
		t.Fatalf("err = %v", err)
	}
}

func TestASourceWithoutASizeIsRefused(t *testing.T) {
	source := parse.StringSource("a.pdf", "x")
	source.Size = -1
	if _, err := New(context.Background(), nil, "file", source); err == nil {
		t.Fatal("accepted")
	}
}
