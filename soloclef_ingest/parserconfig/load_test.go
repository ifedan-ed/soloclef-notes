package parserconfig

import (
	"context"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/selection"
)

func env(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

// localOnly is the smallest working deployment: the parser service and
// nothing hosted.
func localOnly() map[string]string {
	return map[string]string{
		DefaultParser:       "local",
		MaxTextBytes:        "1048576",
		MaxMarkdownBytes:    "8388608",
		LocalURL:            "http://parse-service:8080",
		LocalTimeoutSeconds: "400",
	}
}

func with(base map[string]string, extra map[string]string) map[string]string {
	for name, value := range extra {
		base[name] = value
	}
	return base
}

func available(t *testing.T, registry *selection.Registry) map[parse.ID]bool {
	t.Helper()
	result := map[parse.ID]bool{}
	for _, option := range registry.Options() {
		result[option.Parser] = option.Available
		if len(option.Extensions) == 0 {
			t.Errorf("%s lists no extensions", option.Parser)
		}
	}
	return result
}

func TestAParserWithoutAKeyIsListedAsUnavailable(t *testing.T) {
	registry, err := Load(env(localOnly()))
	if err != nil {
		t.Fatal(err)
	}
	got := available(t, registry)
	want := map[parse.ID]bool{parse.Local: true, parse.Datalab: false, parse.LlamaParse: false, parse.Unstructured: false}
	for id, expected := range want {
		if got[id] != expected {
			t.Errorf("%s available = %v", id, got[id])
		}
	}
	if registry.Default().Parser() != parse.Local {
		t.Errorf("default = %v", registry.Default())
	}

	// Choosing one that is not configured is refused, not rerouted to Local.
	chosen, _ := selection.Of("datalab")
	if _, err := registry.Route(chosen, "guide.pdf"); parse.KindOf(err) != parse.NotConfigured {
		t.Fatalf("err = %v", err)
	}
}

func TestEveryParserConfigured(t *testing.T) {
	registry, err := Load(env(with(localOnly(), map[string]string{
		DefaultParser:        "llamaparse",
		DatalabAPIKey:        "dl-key",
		DatalabMode:          "balanced",
		LlamaParseAPIKey:     "llx-key",
		LlamaParseBaseURL:    "https://api.cloud.eu.llamaindex.ai",
		LlamaParseTier:       "agentic",
		LlamaParseVersion:    "latest",
		UnstructuredAPIKey:   "un-key",
		UnstructuredURL:      "https://api.unstructuredapp.io/general/v0/general",
		UnstructuredStrategy: "hi_res",
	})))
	if err != nil {
		t.Fatal(err)
	}
	for id, isAvailable := range available(t, registry) {
		if !isAvailable {
			t.Errorf("%s is unavailable", id)
		}
	}
	if registry.Default().Parser() != parse.LlamaParse {
		t.Errorf("default = %v", registry.Default())
	}
	for name, want := range map[string]parse.ID{"a.pdf": parse.Unstructured, "a.eml": parse.Unstructured} {
		chosen, _ := selection.Of("unstructured")
		parser, err := registry.Route(chosen, name)
		if err != nil || parser.ID() != want {
			t.Errorf("%s: parser = %v, err = %v", name, parser, err)
		}
	}
}

func TestTheBuiltinParsersAreWired(t *testing.T) {
	registry, err := Load(env(localOnly()))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"note.md":   "# Note\n",
		"data.csv":  "| a | b |\n| --- | --- |\n| 1 | 2 |\n",
		"data.tsv":  "| a | b |\n| --- | --- |\n| 1 | 2 |\n",
		"page.html": "Hello\n",
	}
	inputs := map[string]string{
		"note.md": "# Note\n", "data.csv": "a,b\n1,2\n", "data.tsv": "a\tb\n1\t2\n", "page.html": "<p>Hello</p>",
	}
	for name, want := range cases {
		document, err := registry.Parse(context.Background(), registry.Default(), parse.StringSource(name, inputs[name]))
		if err != nil || document.Markdown != want || document.Parser != parse.Builtin {
			t.Errorf("%s: document = %+v, err = %v", name, document, err)
		}
	}
}

func TestAMissingOrUnusableSettingIsNamed(t *testing.T) {
	cases := []struct {
		name   string
		change map[string]string
		says   string
	}{
		{"no default", map[string]string{DefaultParser: ""}, DefaultParser + " is not set"},
		{"unknown default", map[string]string{DefaultParser: "cloud"}, DefaultParser},
		{"default not configured", map[string]string{DefaultParser: "datalab"}, "not configured"},
		{"no text limit", map[string]string{MaxTextBytes: ""}, MaxTextBytes},
		{"bad text limit", map[string]string{MaxTextBytes: "10MB"}, MaxTextBytes},
		{"no markdown limit", map[string]string{MaxMarkdownBytes: ""}, MaxMarkdownBytes},
		{"local without timeout", map[string]string{LocalTimeoutSeconds: ""}, LocalTimeoutSeconds},
		{"local bad address", map[string]string{LocalURL: "parse-service"}, LocalURL},
		{"datalab without mode", map[string]string{DatalabAPIKey: "k"}, DatalabMode + " is not set, and " + DatalabAPIKey + " is"},
		{"datalab bad mode", map[string]string{DatalabAPIKey: "k", DatalabMode: "best"}, DatalabMode},
		{"llamaparse without region", map[string]string{LlamaParseAPIKey: "k", LlamaParseTier: "agentic", LlamaParseVersion: "latest"}, LlamaParseBaseURL},
		{"llamaparse without tier", map[string]string{LlamaParseAPIKey: "k", LlamaParseBaseURL: "https://api.cloud.llamaindex.ai", LlamaParseVersion: "latest"}, LlamaParseTier},
		{"llamaparse without version", map[string]string{LlamaParseAPIKey: "k", LlamaParseBaseURL: "https://api.cloud.llamaindex.ai", LlamaParseTier: "agentic"}, LlamaParseVersion},
		{"unstructured without url", map[string]string{UnstructuredAPIKey: "k", UnstructuredStrategy: "auto"}, UnstructuredURL},
		{"unstructured without strategy", map[string]string{UnstructuredAPIKey: "k", UnstructuredURL: "https://api.unstructuredapp.io/general/v0/general"}, UnstructuredStrategy},
	}
	for _, tc := range cases {
		_, err := Load(env(with(localOnly(), tc.change)))
		if err == nil || !strings.Contains(err.Error(), tc.says) {
			t.Errorf("%s: err = %v, want it to say %q", tc.name, err, tc.says)
		}
	}
}

func TestNoKeyEverAppearsInAnError(t *testing.T) {
	_, err := Load(env(with(localOnly(), map[string]string{DatalabAPIKey: "dl-secret-value", DatalabMode: "best"})))
	if err == nil || strings.Contains(err.Error(), "dl-secret-value") {
		t.Fatalf("err = %v", err)
	}
}
