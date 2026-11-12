package parserchoice_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice/parserchoicetest"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

func TestChoosingAParser(t *testing.T) {
	// Local is the default; Datalab is configured; the other two are not.
	registry := parserchoicetest.Registry(t, parse.Local, parse.Datalab)

	for _, test := range []struct {
		name, submitted string
		chosen          string
		// refused is a piece of the refusal; "" means accepted.
		refused string
	}{
		{name: "no parser named is the deployment's default", submitted: "", chosen: "local"},
		{name: "a configured parser is the one chosen", submitted: "datalab", chosen: "datalab"},
		{name: "a parser with no key is refused by name", submitted: "llamaparse", refused: "The llamaparse parser is not configured on this server"},
		{name: "another parser with no key is refused by its own name", submitted: "unstructured", refused: "The unstructured parser is not configured"},
		{name: "the retired choice is not a parser", submitted: "cloud", refused: `"cloud" is not a parser. Choose one of: local, datalab, llamaparse, unstructured.`},
		{name: "a name is exact, not folded", submitted: "Local", refused: `"Local" is not a parser`},
		{name: "the word default is not a spelling of the default", submitted: "default", refused: `"default" is not a parser`},
	} {
		t.Run(test.name, func(t *testing.T) {
			chosen, refusal := parserchoice.Choose(registry, test.submitted)
			if test.refused != "" {
				if refusal == nil || !strings.Contains(refusal.Message, test.refused) {
					t.Fatalf("refusal = %v, want one saying %q", refusal, test.refused)
				}
				return
			}
			if refusal != nil {
				t.Fatalf("refused: %s", refusal.Message)
			}
			if chosen.String() != test.chosen {
				t.Errorf("chose %q, want %q", chosen, test.chosen)
			}
		})
	}
}

func TestWhetherTheChosenParserReadsAFile(t *testing.T) {
	registry := parserchoicetest.Registry(t, parse.Local, parse.LlamaParse)
	local, _ := parserchoice.Choose(registry, "local")
	llama, _ := parserchoice.Choose(registry, "llamaparse")

	for _, test := range []struct {
		name, file string
		refused    string
		byLlama    bool
	}{
		{name: "a format the parser reads", file: "thesis.pdf"},
		{name: "the extension's case does not matter", file: "THESIS.PDF"},
		{name: "a format only another parser reads lists what this one reads", file: "deck.pptx",
			refused: `"deck.pptx" cannot be read by the local parser: .pptx is not read by this parser, which reads .pdf, .docx, .epub.`},
		{name: "the same file is read once its parser is chosen", file: "deck.pptx", byLlama: true},
		{name: "text is read by Go whatever parser was chosen", file: "notes.md", byLlama: true},
		{name: "a table is read by Go whatever parser was chosen", file: "figures.csv"},
		{name: "a file with no extension is refused, and says so", file: "README",
			refused: "a file with no extension is not read by this parser"},
	} {
		t.Run(test.name, func(t *testing.T) {
			chosen := local
			if test.byLlama {
				chosen = llama
			}
			refusal, err := parserchoice.Reads(registry, chosen, test.file)
			if err != nil {
				t.Fatal(err)
			}
			if test.refused == "" {
				if refusal != nil {
					t.Fatalf("refused: %s", refusal.Message)
				}
				return
			}
			if refusal == nil || !strings.Contains(refusal.Message, test.refused) {
				t.Fatalf("refusal = %v, want one saying %q", refusal, test.refused)
			}
		})
	}
}

// served is the capabilities route behind a stand-in for the tier's gate.
// vision is the caller's vision check: whether they have a model, and
// who it was asked about.
type vision struct {
	available bool
	err       error
	asked     []string
}

func (v *vision) Available(_ context.Context, userID string) (bool, error) {
	v.asked = append(v.asked, userID)
	return v.available, v.err
}

func served(t *testing.T, signedIn bool) http.Handler {
	return servedWith(t, signedIn, &vision{})
}

func servedWith(t *testing.T, signedIn bool, check *vision) http.Handler {
	t.Helper()
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	route := parserchoice.CapabilitiesRoute{
		Parsers: parserchoicetest.Registry(t, parse.Datalab, parse.Local),
		Limits:  parserchoice.Limits{MaxFileBytes: 50 << 20, MaxFiles: 20},
		Vision:  check,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := route.Register(api); err != nil {
		t.Fatal(err)
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if signedIn {
			request = request.WithContext(principal.WithPrincipal(request.Context(),
				principal.Principal{UserID: "11111111-2222-4333-8444-555555555555", Method: principal.MethodSession}))
		}
		api.Adapter().ServeHTTP(response, request)
	})
}

func TestTheCapabilitiesAPickerReads(t *testing.T) {
	recorder := httptest.NewRecorder()
	served(t, true).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, parserchoice.CapabilitiesPath, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body)
	}

	var body struct {
		DefaultParser string `json:"default_parser"`
		Parsers       []struct {
			Parser             string   `json:"parser"`
			Available          bool     `json:"available"`
			UnavailableReason  *string  `json:"unavailable_reason"`
			DocumentExtensions []string `json:"document_extensions"`
		} `json:"parsers"`
		AlwaysHandled struct {
			Plaintext     []string `json:"plaintext"`
			DirectConvert []string `json:"direct_convert"`
		} `json:"always_handled"`
		VisionAvailable *bool `json:"vision_available"`
		MaxFileBytes    int64 `json:"max_file_size_bytes"`
		MaxFiles        int   `json:"max_files_per_upload"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}

	if body.DefaultParser != "datalab" {
		t.Errorf("default parser %q, want the one the deployment named", body.DefaultParser)
	}
	// All four, in picker order, whether configured or not.
	var order []string
	for _, parser := range body.Parsers {
		order = append(order, parser.Parser)
		configured := parser.Parser == "local" || parser.Parser == "datalab"
		switch {
		case parser.Available != configured:
			t.Errorf("%s: available = %v", parser.Parser, parser.Available)
		case configured && parser.UnavailableReason != nil:
			t.Errorf("%s is available and carries the reason %q", parser.Parser, *parser.UnavailableReason)
		case !configured && (parser.UnavailableReason == nil || *parser.UnavailableReason != "not_configured"):
			t.Errorf("%s is unavailable without saying it is not configured", parser.Parser)
		}
		// A parser that is not configured still says what it would read.
		if len(parser.DocumentExtensions) == 0 {
			t.Errorf("%s lists no extensions", parser.Parser)
		}
	}
	if want := []string{"local", "datalab", "llamaparse", "unstructured"}; !reflect.DeepEqual(order, want) {
		t.Errorf("parsers listed as %v, want %v", order, want)
	}
	if got, want := body.Parsers[0].DocumentExtensions, []string{".docx", ".epub", ".pdf"}; !reflect.DeepEqual(got, want) {
		t.Errorf("local reads %v, want %v (sorted)", got, want)
	}

	// What Go reads itself, in the two groups the picker shows.
	if !contains(body.AlwaysHandled.Plaintext, ".md") || !contains(body.AlwaysHandled.Plaintext, ".txt") {
		t.Errorf("plaintext %v lacks .md or .txt", body.AlwaysHandled.Plaintext)
	}
	if got, want := body.AlwaysHandled.DirectConvert, []string{".csv", ".htm", ".html", ".tsv", ".xhtml"}; !reflect.DeepEqual(got, want) {
		t.Errorf("converted directly: %v, want %v", got, want)
	}
	if contains(body.AlwaysHandled.Plaintext, ".csv") {
		t.Error(".csv is listed as plain text; it is converted")
	}

	if body.VisionAvailable == nil || *body.VisionAvailable {
		t.Error("vision_available must be present and false for a caller with no vision model")
	}
	if body.MaxFileBytes != 50<<20 || body.MaxFiles != 20 {
		t.Errorf("limits %d bytes, %d files", body.MaxFileBytes, body.MaxFiles)
	}
	if kind := recorder.Header().Get("Content-Type"); kind != "application/json" {
		t.Errorf("Content-Type %q", kind)
	}
}

func TestTheCapabilitiesAreNotAnsweredWithoutACaller(t *testing.T) {
	recorder := httptest.NewRecorder()
	served(t, false).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, parserchoice.CapabilitiesPath, nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status %d; a request that slipped past the gate must not be answered", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "datalab") {
		t.Error("the refusal carries the capabilities")
	}
}

func contains(list []string, wanted string) bool {
	for _, item := range list {
		if item == wanted {
			return true
		}
	}
	return false
}

func TestVisionIsOfferedToACallerWhoHasAModel(t *testing.T) {
	check := &vision{available: true}
	recorder := httptest.NewRecorder()
	servedWith(t, true, check).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, parserchoice.CapabilitiesPath, nil))
	var body struct {
		VisionAvailable   bool     `json:"vision_available"`
		PictureExtensions []string `json:"picture_extensions"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body)
	}
	if !body.VisionAvailable || !contains(body.PictureExtensions, ".png") || !contains(body.PictureExtensions, ".heic") {
		t.Errorf("body = %+v", body)
	}
	if !reflect.DeepEqual(check.asked, []string{"11111111-2222-4333-8444-555555555555"}) {
		t.Errorf("asked about %v, want the caller", check.asked)
	}
}

func TestAVisionChoiceThatCannotBeReadIsAnErrorNotANo(t *testing.T) {
	recorder := httptest.NewRecorder()
	servedWith(t, true, &vision{err: errors.New("database down")}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, parserchoice.CapabilitiesPath, nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body)
	}
}
