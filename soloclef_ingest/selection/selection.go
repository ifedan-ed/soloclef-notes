// Package selection is the per-source parser choice: the value a source
// stores, and the registry that turns it into the parser that reads a file.
//
// A source is whatever brings files in: an upload, a notebook's "add
// sources", a Nextcloud import, a Drive connection. Each one names one
// parser, and that parser reads its documents. Nothing here ever uses a
// different parser than the one named: a parser with no key is offered as
// unavailable and refused if chosen anyway, and a file the chosen parser
// does not read is refused with the list of what it does read.
package selection

import (
	"fmt"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

// Selection is one source's parser choice. The zero value is not a choice;
// a Selection comes from Of or from decoding a stored value.
type Selection struct {
	parser parse.ID
}

// choices are the parsers a person can pick, in the order a picker shows
// them: the one that runs here first, then the hosted ones by name.
var choices = []parse.ID{parse.Local, parse.Datalab, parse.LlamaParse, parse.Unstructured}

// Of reads a stored or submitted value. Only the four exact names are
// accepted: no case folding, no aliases and no "default" spelling, so a
// value that reaches the database is one the registry can route.
func Of(value string) (Selection, error) {
	for _, choice := range choices {
		if value == string(choice) {
			return Selection{parser: choice}, nil
		}
	}
	return Selection{}, fmt.Errorf("selection: %q is not a parser; choose local, datalab, llamaparse or unstructured", value)
}

// Parser is the chosen parser's ID, "" for the zero Selection.
func (s Selection) Parser() parse.ID { return s.parser }

// String is the value to store.
func (s Selection) String() string { return string(s.parser) }

// MarshalText and UnmarshalText make a Selection a plain string in JSON and
// in a text column, validated on the way in.
func (s Selection) MarshalText() ([]byte, error) {
	if s.parser == "" {
		return nil, fmt.Errorf("selection: no parser was chosen")
	}
	return []byte(s.parser), nil
}

func (s *Selection) UnmarshalText(text []byte) error {
	chosen, err := Of(string(text))
	if err != nil {
		return err
	}
	*s = chosen
	return nil
}
