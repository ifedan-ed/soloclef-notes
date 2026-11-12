package selection

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

func must(t *testing.T, value string) Selection {
	t.Helper()
	chosen, err := Of(value)
	if err != nil {
		t.Fatal(err)
	}
	return chosen
}

func TestOnlyTheFourExactNamesAreAChoice(t *testing.T) {
	for _, value := range []string{"local", "datalab", "llamaparse", "unstructured"} {
		if chosen, err := Of(value); err != nil || chosen.String() != value {
			t.Errorf("Of(%q) = %v, %v", value, chosen, err)
		}
	}
	// The retired spellings and near misses are not quietly understood.
	for _, value := range []string{"", "cloud", "basic", "premium", "LOCAL", " local", "llamacloud", "builtin"} {
		if _, err := Of(value); err == nil {
			t.Errorf("Of(%q) was accepted", value)
		}
	}
}

func TestASelectionIsAPlainStringInJSONAndValidatedOnTheWayIn(t *testing.T) {
	type source struct {
		Parser Selection `json:"parser"`
	}
	encoded, err := json.Marshal(source{Parser: must(t, "datalab")})
	if err != nil || string(encoded) != `{"parser":"datalab"}` {
		t.Fatalf("encoded = %s, err = %v", encoded, err)
	}
	var decoded source
	if err := json.Unmarshal([]byte(`{"parser":"llamaparse"}`), &decoded); err != nil || decoded.Parser.Parser() != parse.LlamaParse {
		t.Fatalf("decoded = %+v, err = %v", decoded, err)
	}
	if err := json.Unmarshal([]byte(`{"parser":"cloud"}`), &decoded); err == nil {
		t.Fatal("an unknown parser was decoded")
	}
	if _, err := json.Marshal(source{}); err == nil {
		t.Fatal("a source with no choice was encoded")
	}
}

// fake is a parser that records what it was asked to read.
type fake struct {
	id         parse.ID
	extensions []string
	parsed     []string
}

func (f *fake) ID() parse.ID { return f.id }
func (f *fake) Reads(extension string) bool {
	for _, known := range f.extensions {
		if known == extension {
			return true
		}
	}
	return false
}
func (f *fake) Parse(_ context.Context, source parse.Source) (parse.Document, error) {
	f.parsed = append(f.parsed, source.Name)
	return parse.Document{Markdown: "read by " + string(f.id), Parser: f.id}, nil
}

// deployment is a registry as a server with the parser service and a Datalab
// key has it: two parsers configured, two not.
func deployment(t *testing.T) (*Registry, *fake, *fake, *fake) {
	t.Helper()
	text := &fake{id: parse.Builtin, extensions: []string{".md", ".txt", ".csv", ".html"}}
	local := &fake{id: parse.Local, extensions: []string{".pdf", ".docx"}}
	datalab := &fake{id: parse.Datalab, extensions: []string{".pdf", ".xlsx"}}
	registry, err := NewRegistry([]parse.Parser{text}, []Choice{
		{ID: parse.Local, Extensions: local.extensions, Parser: local},
		{ID: parse.Datalab, Extensions: datalab.extensions, Parser: datalab},
		{ID: parse.LlamaParse, Extensions: []string{".pdf", ".hwp"}},
		{ID: parse.Unstructured, Extensions: []string{".pdf", ".eml"}},
	}, must(t, "local"))
	if err != nil {
		t.Fatal(err)
	}
	return registry, text, local, datalab
}

func TestOptionsListsEveryParserAndSaysWhichCannotRun(t *testing.T) {
	registry, _, _, _ := deployment(t)

	options := registry.Options()
	encoded, _ := json.Marshal(options)
	want := `[` +
		`{"parser":"local","available":true,"extensions":[".pdf",".docx"]},` +
		`{"parser":"datalab","available":true,"extensions":[".pdf",".xlsx"]},` +
		`{"parser":"llamaparse","available":false,"unavailable_reason":"not_configured","extensions":[".pdf",".hwp"]},` +
		`{"parser":"unstructured","available":false,"unavailable_reason":"not_configured","extensions":[".pdf",".eml"]}` +
		`]`
	if string(encoded) != want {
		t.Fatalf("options:\n%s\nwant:\n%s", encoded, want)
	}
	if registry.Default() != must(t, "local") {
		t.Errorf("default = %v", registry.Default())
	}
}

func TestADocumentGoesToTheChosenParserAndOnlyToIt(t *testing.T) {
	registry, _, local, datalab := deployment(t)

	document, err := registry.Parse(context.Background(), must(t, "datalab"), parse.StringSource("guide.pdf", "x"))
	if err != nil || document.Parser != parse.Datalab {
		t.Fatalf("document = %+v, err = %v", document, err)
	}
	if len(datalab.parsed) != 1 || len(local.parsed) != 0 {
		t.Fatalf("datalab read %v, local read %v", datalab.parsed, local.parsed)
	}
}

func TestAParserThatIsNotConfiguredIsRefusedNotSwapped(t *testing.T) {
	registry, _, local, datalab := deployment(t)

	// Local and Datalab could both read this PDF. Neither is asked.
	_, err := registry.Parse(context.Background(), must(t, "llamaparse"), parse.StringSource("guide.pdf", "x"))
	if parse.KindOf(err) != parse.NotConfigured {
		t.Fatalf("err = %v", err)
	}
	if len(local.parsed)+len(datalab.parsed) != 0 {
		t.Fatalf("another parser read the file: local %v, datalab %v", local.parsed, datalab.parsed)
	}
}

func TestAFileTheChosenParserDoesNotReadIsRefusedWithWhatItDoesRead(t *testing.T) {
	registry, _, _, datalab := deployment(t)

	// Datalab reads .xlsx here; Local was chosen, so the file is refused.
	_, err := registry.Parse(context.Background(), must(t, "local"), parse.StringSource("sheet.xlsx", "x"))
	if parse.KindOf(err) != parse.UnsupportedFormat || !strings.Contains(err.Error(), ".xlsx is not read by this parser, which reads .pdf, .docx") {
		t.Fatalf("err = %v", err)
	}
	if len(datalab.parsed) != 0 {
		t.Fatal("the file went to a parser nobody chose")
	}

	_, err = registry.Route(must(t, "local"), "README")
	if parse.KindOf(err) != parse.UnsupportedFormat || !strings.Contains(err.Error(), "a file with no extension") {
		t.Fatalf("no extension: err = %v", err)
	}
}

func TestTextFormatsAreReadInGoWhateverWasChosen(t *testing.T) {
	registry, text, _, datalab := deployment(t)

	for _, name := range []string{"note.md", "data.csv", "page.html"} {
		// Even a choice that is not configured: no parser is needed here.
		for _, chosen := range []string{"datalab", "llamaparse"} {
			parser, err := registry.Route(must(t, chosen), name)
			if err != nil || parser.ID() != parse.Builtin {
				t.Errorf("%s with %s: parser = %v, err = %v", name, chosen, parser, err)
			}
		}
	}
	if _, err := registry.Parse(context.Background(), must(t, "datalab"), parse.StringSource("note.md", "x")); err != nil {
		t.Fatal(err)
	}
	if len(text.parsed) != 1 || len(datalab.parsed) != 0 {
		t.Fatalf("text read %v, datalab read %v", text.parsed, datalab.parsed)
	}
}

func TestTheZeroSelectionIsAnErrorNotADefault(t *testing.T) {
	registry, _, local, _ := deployment(t)
	if _, err := registry.Route(Selection{}, "guide.pdf"); err == nil || len(local.parsed) != 0 {
		t.Fatalf("err = %v", err)
	}
}

func TestARegistryMustBeComplete(t *testing.T) {
	local := &fake{id: parse.Local}
	all := []Choice{{ID: parse.Local, Parser: local}, {ID: parse.Datalab}, {ID: parse.LlamaParse}, {ID: parse.Unstructured}}

	if _, err := NewRegistry(nil, all[:3], must(t, "local")); err == nil || !strings.Contains(err.Error(), "unstructured") {
		t.Errorf("a missing choice: err = %v", err)
	}
	if _, err := NewRegistry(nil, all, must(t, "datalab")); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("an unconfigured default: err = %v", err)
	}
	mislabelled := append([]Choice{{ID: parse.Datalab, Parser: local}}, all[0], all[2], all[3])
	if _, err := NewRegistry(nil, mislabelled, must(t, "local")); err == nil || !strings.Contains(err.Error(), "calls itself") {
		t.Errorf("a mislabelled parser: err = %v", err)
	}
	extra := append(all[:4:4], Choice{ID: parse.Builtin})
	if _, err := NewRegistry(nil, extra, must(t, "local")); err == nil {
		t.Error("a fifth choice was accepted")
	}
}
