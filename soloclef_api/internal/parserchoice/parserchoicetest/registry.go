// Package parserchoicetest builds a real selection.Registry for tests: the
// registry's own rules decide every answer, and only the parsers behind it
// are stand-ins that never parse anything.
package parserchoicetest

import (
	"context"
	"errors"
	"slices"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/filekind"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/selection"
)

// Extensions is what each stand-in parser reads. They differ on purpose, so
// a test can pick a file one parser reads and another does not.
var Extensions = map[parse.ID][]string{
	parse.Local:        {".pdf", ".docx", ".epub"},
	parse.Datalab:      {".pdf", ".docx", ".png"},
	parse.LlamaParse:   {".pdf", ".pptx"},
	parse.Unstructured: {".pdf", ".msg"},
}

// stand is a parser that knows what it reads and refuses to be run.
type stand struct {
	id    parse.ID
	reads func(extension string) bool
}

func (s stand) ID() parse.ID                { return s.id }
func (s stand) Reads(extension string) bool { return s.reads(extension) }
func (s stand) Parse(context.Context, parse.Source) (parse.Document, error) {
	return parse.Document{}, errors.New("parserchoicetest: the API tier never parses")
}

// Registry is a registry in which only the named parsers are configured.
// The first one named is the default. Text, CSV, TSV and HTML are read by
// a builtin, as in a real deployment.
func Registry(t testing.TB, configured ...parse.ID) *selection.Registry {
	t.Helper()
	if len(configured) == 0 {
		t.Fatal("parserchoicetest: name at least one configured parser; the first is the default")
	}
	builtin := stand{id: parse.Builtin, reads: func(extension string) bool {
		return filekind.Of(extension) != filekind.Document
	}}
	var choices []selection.Choice
	for _, id := range []parse.ID{parse.Local, parse.Datalab, parse.LlamaParse, parse.Unstructured} {
		choice := selection.Choice{ID: id, Extensions: Extensions[id]}
		if slices.Contains(configured, id) {
			extensions := Extensions[id]
			choice.Parser = stand{id: id, reads: func(extension string) bool { return slices.Contains(extensions, extension) }}
		}
		choices = append(choices, choice)
	}
	initial, err := selection.Of(string(configured[0]))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := selection.NewRegistry([]parse.Parser{builtin}, choices, initial)
	if err != nil {
		t.Fatal(err)
	}
	return registry
}
