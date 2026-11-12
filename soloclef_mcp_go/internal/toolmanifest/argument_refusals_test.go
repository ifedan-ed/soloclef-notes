package toolmanifest_test

import (
	"encoding/json"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/mcpserver"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/settings"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/toolschema"
)

// refusals holds, per case, the arguments sent and the sentence the server
// answers. First captured from the Python server it replaced, and kept. The
// list-element cases on soloclef_list_documents were written later in the same
// pydantic form, when the scraper tools that used to carry them were removed,
// so binding a list still has a case per wrong element. Captured over HTTP
// rather than written from pydantic's documentation, because the model reads these sentences and several
// of them are not what the documentation implies — " 3 " is an integer, "2.0" is
// an integer, "2.5" is a parse failure rather than a fractional-part failure,
// and a value whose repr runs past 50 characters is cut to 25 + "..." + 24.
const refusals = "testdata/argument_refusals.json"

type refusalCase struct {
	Label     string         `json:"label"`
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
	Refusal   string         `json:"refusal"`
}

func TestArgumentRefusalsAreThePublishedSentences(t *testing.T) {
	var cases []refusalCase
	readJSON(t, refusals, &cases)
	if len(cases) == 0 {
		t.Fatal("no captured refusals to check against")
	}
	signatures := signaturesByTool(t)

	for _, each := range cases {
		t.Run(each.Label, func(t *testing.T) {
			signature, known := signatures[each.Tool]
			if !known {
				t.Fatalf("no signature for %s", each.Tool)
			}
			raw, err := json.Marshal(each.Arguments)
			if err != nil {
				t.Fatal(err)
			}
			_, err = signature.Bind(raw)
			if err == nil {
				t.Fatalf("accepted arguments that should be refused with:\n%s",
					each.Refusal)
			}
			if err.Error() != each.Refusal {
				t.Errorf("refusal differs\n got:\n%s\nwant:\n%s", err.Error(), each.Refusal)
			}
		})
	}
}

func TestAcceptedArgumentsCarryTheCoercedValue(t *testing.T) {
	signatures := signaturesByTool(t)
	search := signatures["soloclef_search_knowledge_base"]
	flagged := signatures["soloclef_pubmed_search"]

	numbers := map[string]int{
		`{"query":"x","top_k":3}`:     3,
		`{"query":"x","top_k":"3"}`:   3,
		`{"query":"x","top_k":3.0}`:   3,
		`{"query":"x","top_k":"2.0"}`: 2,
		`{"query":"x","top_k":" 3 "}`: 3,
		`{"query":"x","top_k":"+3"}`:  3,
		`{"query":"x","top_k":"1_0"}`: 10,
		`{"query":"x","top_k":true}`:  1,
		`{"query":"x"}`:               5,
	}
	for arguments, want := range numbers {
		bound, err := search.Bind([]byte(arguments))
		if err != nil {
			t.Fatalf("%s: %v", arguments, err)
		}
		if got := bound.Number("top_k"); got != want {
			t.Errorf("%s: top_k is %d, want %d", arguments, got, want)
		}
	}

	flags := map[string]bool{
		`{"query":"x","full_text":true}`:   true,
		`{"query":"x","full_text":"yes"}`:  true,
		`{"query":"x","full_text":"TRUE"}`: true,
		`{"query":"x","full_text":"off"}`:  false,
		`{"query":"x","full_text":"N"}`:    false,
		`{"query":"x","full_text":1}`:      true,
		`{"query":"x","full_text":1.0}`:    true,
		`{"query":"x"}`:                    false,
	}
	for arguments, want := range flags {
		bound, err := flagged.Bind([]byte(arguments))
		if err != nil {
			t.Fatalf("%s: %v", arguments, err)
		}
		if got := bound.Flag("full_text"); got != want {
			t.Errorf("%s: full_text is %v, want %v", arguments, got, want)
		}
	}
}

func TestAnUnknownArgumentIsIgnored(t *testing.T) {
	signature := signaturesByTool(t)["soloclef_search_knowledge_base"]
	bound, err := signature.Bind([]byte(`{"query":"x","nope":1}`))
	if err != nil {
		t.Fatalf("an extra argument should be ignored, not refused: %v", err)
	}
	if bound.Text("query") != "x" {
		t.Errorf("query is %q", bound.Text("query"))
	}
}

func TestAbsentArgumentsObjectIsAnEmptyOne(t *testing.T) {
	signature := signaturesByTool(t)["soloclef_search_knowledge_base"]
	for _, arguments := range []string{"", "null", "{}"} {
		_, err := signature.Bind([]byte(arguments))
		if err == nil {
			t.Fatalf("%q: a missing required argument should be refused", arguments)
		}
		const want = "input_value={}, input_type=dict"
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q: refusal does not report an empty dict:\n%s", arguments, err.Error())
		}
	}
}

func signaturesByTool(t *testing.T) map[string]toolschema.Signature {
	t.Helper()
	definitions, _ := mcpserver.Definitions(settings.Settings{
		BaseURL:        "http://localhost:8000",
		APIPrefix:      "/api/v1",
		TimeoutSeconds: 5,
	})
	signatures := map[string]toolschema.Signature{}
	for _, definition := range definitions {
		signatures[definition.Name] = definition.Signature
	}
	return signatures
}
