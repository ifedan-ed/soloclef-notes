package selection

import (
	"context"
	"fmt"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/filekind"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

// NotConfigured is why an Option is unavailable: a stable token the web app
// translates, not a sentence.
const NotConfigured = "not_configured"

// Choice is one selectable parser as this deployment has it.
type Choice struct {
	ID parse.ID
	// Extensions is what the parser reads, known whether or not it is
	// configured, so a picker can show what a missing parser would offer.
	Extensions []string
	// Parser is the configured parser, or nil when it has no key or address.
	Parser parse.Parser
}

// Option is one entry of a source's parser picker.
type Option struct {
	Parser    parse.ID `json:"parser"`
	Available bool     `json:"available"`
	// UnavailableReason is NotConfigured when Available is false, else "".
	UnavailableReason string   `json:"unavailable_reason,omitempty"`
	Extensions        []string `json:"extensions"`
}

// Registry knows which parsers are configured and routes a file to the one
// its source chose.
type Registry struct {
	choices  map[parse.ID]Choice
	builtins []parse.Parser
	initial  Selection
}

// NewRegistry builds a Registry.
//
// builtins are the parsers for formats read in Go; a file one of them reads
// never reaches a chosen parser. available must hold all four choices,
// configured or not. initial is the choice a picker starts on and the one
// applied to a request that names none; it must be configured, because a
// default that cannot run would fail every such request.
func NewRegistry(builtins []parse.Parser, available []Choice, initial Selection) (*Registry, error) {
	registry := &Registry{choices: map[parse.ID]Choice{}, builtins: builtins, initial: initial}
	for _, choice := range available {
		if choice.Parser != nil && choice.Parser.ID() != choice.ID {
			return nil, fmt.Errorf("selection: the parser given for %q calls itself %q", choice.ID, choice.Parser.ID())
		}
		registry.choices[choice.ID] = choice
	}
	for _, id := range choices {
		if _, present := registry.choices[id]; !present {
			return nil, fmt.Errorf("selection: the %s parser is missing from the registry; pass it unconfigured rather than leaving it out", id)
		}
	}
	if len(registry.choices) != len(choices) {
		return nil, fmt.Errorf("selection: the registry was given a parser nobody can choose")
	}
	if registry.choices[initial.parser].Parser == nil {
		return nil, fmt.Errorf("selection: the default parser %q is not configured", initial)
	}
	return registry, nil
}

// Options lists all four parsers in picker order, configured or not. A
// parser with no key is shown as unavailable, never left out and never
// replaced.
func (r *Registry) Options() []Option {
	options := make([]Option, 0, len(choices))
	for _, id := range choices {
		choice := r.choices[id]
		option := Option{Parser: id, Available: choice.Parser != nil, Extensions: choice.Extensions}
		if !option.Available {
			option.UnavailableReason = NotConfigured
		}
		options = append(options, option)
	}
	return options
}

// Default is the choice a picker starts on.
func (r *Registry) Default() Selection { return r.initial }

// Route returns the parser that reads filename for a source that chose
// selection.
//
// Text, Markdown, CSV, TSV and HTML are read in Go whatever was chosen: no
// parser is needed to read text, and sending a text file to a metered API
// would be paying for nothing. Every other file goes to the chosen parser
// and only to it.
func (r *Registry) Route(selection Selection, filename string) (parse.Parser, error) {
	extension := filekind.Extension(filename)
	if filekind.Of(extension) != filekind.Document {
		for _, builtin := range r.builtins {
			if builtin.Reads(extension) {
				return builtin, nil
			}
		}
		return nil, fmt.Errorf("selection: no builtin parser reads %s; the registry was built without one", extension)
	}

	choice, known := r.choices[selection.parser]
	if !known {
		// The zero Selection: a caller that skipped Of.
		return nil, fmt.Errorf("selection: no parser was chosen for %s", filename)
	}
	if choice.Parser == nil {
		return nil, &parse.Error{
			Kind: parse.NotConfigured, Parser: choice.ID,
			Message: "this parser has no key or address on this server; choose another parser for this source or ask the administrator to configure it",
		}
	}
	if !choice.Parser.Reads(extension) {
		shown := extension
		if shown == "" {
			shown = "a file with no extension"
		}
		return nil, &parse.Error{
			Kind: parse.UnsupportedFormat, Parser: choice.ID,
			Message: fmt.Sprintf("%s is not read by this parser, which reads %s", shown, strings.Join(choice.Extensions, ", ")),
		}
	}
	return choice.Parser, nil
}

// Parse routes the source and parses it.
func (r *Registry) Parse(ctx context.Context, selection Selection, source parse.Source) (parse.Document, error) {
	parser, err := r.Route(selection, source.Name)
	if err != nil {
		return parse.Document{}, err
	}
	return parser.Parse(ctx, source)
}
