package fileread

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/internal/testpdf"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/selection"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/textfile"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/vision"
)

// parser is a document parser that records the names it was given and
// answers with them, or with fail.
type parser struct {
	id         parse.ID
	extensions []string
	fail       error
	read       []string
}

func (p *parser) ID() parse.ID { return p.id }
func (p *parser) Reads(extension string) bool {
	return slices.Contains(p.extensions, extension)
}
func (p *parser) Parse(ctx context.Context, source parse.Source) (parse.Document, error) {
	p.read = append(p.read, source.Name)
	if p.fail != nil {
		return parse.Document{}, p.fail
	}
	body, err := source.Open(ctx)
	if err != nil {
		return parse.Document{}, err
	}
	defer body.Close()
	data, _ := io.ReadAll(body)
	return parse.Unpaged(p.id, "parsed "+source.Name+" by "+string(p.id)+" ("+string(rune('0'+len(data)%10))+")")
}

// describer is a vision model that records what it was asked.
type describer struct {
	models  []string
	purpose vision.Purpose
	answer  string
	fail    error
}

// model is the account's vision model by this name, as accountfiles opens
// it: the opener is called only when a picture is to be described.
func (d *describer) model(name string) func(context.Context) (vision.Describer, error) {
	return func(context.Context) (vision.Describer, error) { return named{d, name}, nil }
}

type named struct {
	*describer
	name string
}

func (n named) Describe(_ context.Context, _ vision.Picture, purpose vision.Purpose) (string, error) {
	n.models, n.purpose = append(n.models, n.name), purpose
	return n.answer, n.fail
}

type world struct {
	local, datalab *parser
	vision         *describer
	reader         *Reader
}

func setUp(t *testing.T) world {
	t.Helper()
	local := &parser{id: parse.Local, extensions: []string{".pdf", ".docx", ".pptx", ".epub"}}
	datalab := &parser{id: parse.Datalab, extensions: []string{".pdf", ".docx", ".xlsx"}}
	text, err := textfile.New(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	initial, _ := selection.Of("local")
	registry, err := selection.NewRegistry([]parse.Parser{text}, []selection.Choice{
		{ID: parse.Local, Extensions: local.extensions, Parser: local},
		{ID: parse.Datalab, Extensions: datalab.extensions, Parser: datalab},
		{ID: parse.LlamaParse, Extensions: []string{".pdf"}},
		{ID: parse.Unstructured, Extensions: []string{".pdf"}},
	}, initial)
	if err != nil {
		t.Fatal(err)
	}
	seen := &describer{answer: "A red square."}
	reader, err := New(registry, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	return world{local: local, datalab: datalab, vision: seen, reader: reader}
}

// reason is the Unreadable's reason, failing the test for any other error.
func reason(t *testing.T, err error) string {
	t.Helper()
	var refused *Unreadable
	if !errors.As(err, &refused) {
		t.Fatalf("error = %v, want an Unreadable", err)
	}
	return refused.Reason
}

var png = []byte("\x89PNG\r\n\x1a\n rest of a picture")

func TestADocumentGoesToTheParserTheConnectionChose(t *testing.T) {
	w := setUp(t)
	pdf := testpdf.Document("Invoice")

	read, err := w.reader.Read(context.Background(), File{Name: "invoice.pdf", Data: pdf}, Choices{Parser: "datalab"})
	if err != nil {
		t.Fatal(err)
	}
	if read.By != parse.Datalab || !strings.HasPrefix(read.Markdown, "parsed invoice.pdf by datalab") || read.Cut {
		t.Errorf("read = %+v", read)
	}
	if len(w.local.read) != 0 {
		t.Errorf("the default parser was used as well: %v", w.local.read)
	}

	// No choice on the connection is the deployment's default.
	if read, err = w.reader.Read(context.Background(), File{Name: "invoice.pdf", Data: pdf}, Choices{}); err != nil || read.By != parse.Local {
		t.Fatalf("with no choice: %+v, %v", read, err)
	}
}

func TestANameWithNoExtensionIsRoutedByItsType(t *testing.T) {
	w := setUp(t)
	_, err := w.reader.Read(context.Background(), File{Name: "scan", ContentType: "application/pdf; name=scan", Data: testpdf.Document("x")}, Choices{})
	if err != nil || !slices.Equal(w.local.read, []string{"scan.pdf"}) {
		t.Fatalf("read %v, %v", w.local.read, err)
	}
}

func TestAFormatTheChosenParserDoesNotReadIsRefusedWithWhatItReads(t *testing.T) {
	w := setUp(t)
	_, err := w.reader.Read(context.Background(), File{Name: "sheet.xlsx", Data: zipOf(t, "xl/workbook.xml", "<workbook/>")}, Choices{Parser: "local"})
	why := reason(t, err)
	if !strings.Contains(why, ".xlsx is not read by this parser") || !strings.Contains(why, ".pdf") {
		t.Errorf("reason = %q", why)
	}
	// The same file with a parser that reads it is read.
	if _, err := w.reader.Read(context.Background(), File{Name: "sheet.xlsx", Data: zipOf(t, "xl/workbook.xml", "<workbook/>")}, Choices{Parser: "datalab"}); err != nil {
		t.Fatal(err)
	}
}

func TestAParserThatIsNotSetUpIsSaidSoAndNoOtherIsUsed(t *testing.T) {
	w := setUp(t)
	_, err := w.reader.Read(context.Background(), File{Name: "a.pdf", Data: testpdf.Document("x")}, Choices{Parser: "llamaparse"})
	if why := reason(t, err); !strings.Contains(why, "not set up on this server") {
		t.Errorf("reason = %q", why)
	}
	if len(w.local.read)+len(w.datalab.read) != 0 {
		t.Error("another parser read the file in its place")
	}
}

func TestAPictureIsDescribedByTheAccountsVisionModel(t *testing.T) {
	w := setUp(t)
	// Named and typed as something else: the bytes decide.
	read, err := w.reader.Read(context.Background(), File{Name: "photo.pdf", ContentType: "application/pdf", Data: png}, Choices{Vision: w.vision.model("account-vision")})
	if err != nil || read.Markdown != "A red square." || read.By != parse.Vision {
		t.Fatalf("read = %+v, %v", read, err)
	}
	if !slices.Equal(w.vision.models, []string{"account-vision"}) || w.vision.purpose != vision.Whole {
		t.Errorf("asked %v for %v", w.vision.models, w.vision.purpose)
	}
	if len(w.local.read) != 0 {
		t.Error("a picture went to the document parser")
	}
}

func TestAPictureWithNoVisionModelIsSaidSo(t *testing.T) {
	w := setUp(t)
	_, err := w.reader.Read(context.Background(), File{Name: "photo.png", Data: png}, Choices{})
	if why := reason(t, err); !strings.Contains(why, "no vision model is set") {
		t.Errorf("reason = %q", why)
	}
	if len(w.vision.models) != 0 {
		t.Error("the vision model was asked with no model")
	}
}

func TestAFailureOfTheMomentSaysToTryAgain(t *testing.T) {
	w := setUp(t)
	w.local.fail = &parse.Error{Kind: parse.Unavailable, Parser: parse.Local, Message: "down"}
	_, err := w.reader.Read(context.Background(), File{Name: "a.pdf", Data: testpdf.Document("x")}, Choices{})
	if why := reason(t, err); !strings.Contains(why, "Try again shortly") {
		t.Errorf("reason = %q", why)
	}
	w.vision.fail = &parse.Error{Kind: parse.Unauthorized, Parser: parse.Vision, Message: "no"}
	_, err = w.reader.Read(context.Background(), File{Name: "p.png", Data: png}, Choices{Vision: w.vision.model("m")})
	if why := reason(t, err); !strings.Contains(why, "The vision model refused") {
		t.Errorf("reason = %q", why)
	}
}

// The vision model is opened only for a picture, and what opening it said
// is the reason given.
func TestTheVisionModelIsOpenedOnlyForAPicture(t *testing.T) {
	w := setUp(t)
	opened := 0
	removed := func(context.Context) (vision.Describer, error) {
		opened++
		return nil, &Unreadable{Reason: "The vision model you chose from your own connection has been removed."}
	}
	if _, err := w.reader.Read(context.Background(), File{Name: "a.pdf", Data: testpdf.Document("x")}, Choices{Vision: removed}); err != nil || opened != 0 {
		t.Fatalf("a PDF opened the vision model %d times (%v)", opened, err)
	}
	_, err := w.reader.Read(context.Background(), File{Name: "p.png", Data: png}, Choices{Vision: removed})
	if why := reason(t, err); !strings.Contains(why, "has been removed") || opened != 1 {
		t.Errorf("reason = %q, opened %d times", why, opened)
	}
	// A failure to open that is no sentence is one to try again after.
	broken := func(context.Context) (vision.Describer, error) { return nil, errors.New("the database is away") }
	_, err = w.reader.Read(context.Background(), File{Name: "p.png", Data: png}, Choices{Vision: broken})
	if why := reason(t, err); !strings.Contains(why, "Try again shortly") {
		t.Errorf("reason = %q", why)
	}
	// A request the endpoint will refuse again is not one to try again.
	w.vision.fail = &parse.Error{Kind: parse.Rejected, Parser: parse.Vision, Message: "The address models.lan points inside this server's network"}
	_, err = w.reader.Read(context.Background(), File{Name: "p.png", Data: png}, Choices{Vision: w.vision.model("own:3")})
	if why := reason(t, err); !strings.Contains(why, "points inside this server's network.") || strings.Contains(why, "Try again") {
		t.Errorf("reason = %q", why)
	}
}

func TestTextIsReadAsItIsAndCut(t *testing.T) {
	w := setUp(t)
	read, err := w.reader.Read(context.Background(), File{Name: "notes", ContentType: "text/plain", Data: []byte("plain \xff words")}, Choices{})
	if err != nil || read.Markdown != "plain � words" || read.By != parse.Builtin {
		t.Fatalf("read = %+v, %v", read, err)
	}
	long := strings.Repeat("ä", MaxChars+5)
	read, err = w.reader.Read(context.Background(), File{Name: "long.md", Data: []byte(long)}, Choices{})
	if err != nil || !read.Cut || len([]rune(read.Markdown)) != MaxChars {
		t.Fatalf("a long text: cut %v, %d characters, %v", read.Cut, len([]rune(read.Markdown)), err)
	}
}

func TestTheBoundsRefuseBeforeAnyParserRuns(t *testing.T) {
	w := setUp(t)
	// A member that claims a hundred and one times its compressed size.
	var bomb bytes.Buffer
	archive := zip.NewWriter(&bomb)
	member, _ := archive.Create("word/document.xml")
	_, _ = member.Write(bytes.Repeat([]byte{'a'}, 1<<20))
	_ = archive.Close()

	for name, case_ := range map[string]struct {
		file File
		says string
	}{
		"too large":            {File{Name: "big.pdf", Data: make([]byte, MaxFileBytes+1)}, "over the 25 MB"},
		"empty":                {File{Name: "a.pdf"}, "empty"},
		"a decompression bomb": {File{Name: "a.docx", Data: bomb.Bytes()}, "compressed far more than a document is"},
		"too many pages":       {File{Name: "a.pdf", Data: testpdf.Document(slices.Repeat([]string{"page"}, maxPDFPages+1)...)}, "501 pages"},
		"a broken archive":     {File{Name: "a.docx", Data: []byte("PK\x03\x04 not really")}, "not a valid archive"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := w.reader.Read(context.Background(), case_.file, Choices{})
			if why := reason(t, err); !strings.Contains(why, case_.says) {
				t.Errorf("reason = %q, want it to say %q", why, case_.says)
			}
		})
	}
	if len(w.local.read) != 0 {
		t.Errorf("a parser was given a refused file: %v", w.local.read)
	}
}

func TestTheCallersOwnCancellationIsNotDressedAsAReason(t *testing.T) {
	w := setUp(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.local.fail = context.Canceled
	if _, err := w.reader.Read(ctx, File{Name: "a.pdf", Data: testpdf.Document("x")}, Choices{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

// zipOf is a small, honest zip with one member.
func zipOf(t *testing.T, name, content string) []byte {
	t.Helper()
	var out bytes.Buffer
	archive := zip.NewWriter(&out)
	member, err := archive.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = member.Write([]byte(content))
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
