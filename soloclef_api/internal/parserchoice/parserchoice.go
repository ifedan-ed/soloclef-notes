// Package parserchoice is the parser a source chose, as the API tier checks
// it: the capabilities the pickers read, and the two refusals every route
// that takes a `parser` makes.
//
// # One registry, two tiers
//
// The choice is made here and carried out in the indexing worker, minutes
// later and in another process. If the two disagreed about which parsers
// are configured or which files each reads, an upload would be accepted,
// stored, and then failed by the worker for a reason the route could have
// given at once. So both ask the same selection.Registry
// (soloclef_ingest/parserconfig builds it from the same settings in both
// processes), and this package adds nothing to its answers but wording.
//
// # Nothing is ever swapped
//
// A parser that is not configured is refused by name; a file the chosen
// parser does not read is refused with the list of what it does read. No
// request is quietly sent to a different parser than the one it named.
package parserchoice

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/filekind"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/selection"
)

// Registry is the part of *selection.Registry this tier asks.
type Registry interface {
	// Options is all four parsers, configured or not, in picker order.
	Options() []selection.Option
	// Default is SOLOCLEF_PARSER_DEFAULT: the choice a picker starts on, and
	// the one a request that names no parser gets.
	Default() selection.Selection
	// Route is the parser that would read this file for this choice, or the
	// *parse.Error saying why none will.
	Route(chosen selection.Selection, filename string) (parse.Parser, error)
}

// Refusal is a request this package will not accept: always the caller's to
// fix, so always a 400.
type Refusal struct {
	Message string
}

func (r *Refusal) Error() string { return r.Message }

// Status is the HTTP status of every refusal here.
const Status = http.StatusBadRequest

// Choose reads a submitted `parser` value.
//
// An absent or empty value is the deployment's named default: that is what
// the setting is for, and the capabilities route tells the client which one
// it is, so it is not a guess. Any other value must be one of the four
// exact names and must be configured here.
func Choose(registry Registry, submitted string) (selection.Selection, *Refusal) {
	if submitted == "" {
		return registry.Default(), nil
	}
	chosen, err := selection.Of(submitted)
	if err != nil {
		return selection.Selection{}, &Refusal{Message: fmt.Sprintf(
			"%q is not a parser. Choose one of: %s.", submitted, strings.Join(names(registry), ", "))}
	}
	for _, option := range registry.Options() {
		if option.Parser == chosen.Parser() && !option.Available {
			return selection.Selection{}, &Refusal{Message: fmt.Sprintf(
				"The %s parser is not configured on this server. Choose another parser, or ask the administrator to configure it.",
				chosen)}
		}
	}
	return chosen, nil
}

// Reads reports whether the chosen parser (or Go itself, for text, CSV, TSV
// and HTML) reads a file with this name, by asking the registry the very
// question the worker will ask when it routes the stored file.
//
// A nil error and a nil refusal mean it does. An error is the registry
// misbehaving, which is the server's fault.
func Reads(registry Registry, chosen selection.Selection, filename string) (*Refusal, error) {
	_, err := registry.Route(chosen, filename)
	if err == nil {
		return nil, nil
	}
	var refused *parse.Error
	if !errors.As(err, &refused) {
		return nil, err
	}
	switch refused.Kind {
	case parse.UnsupportedFormat:
		return &Refusal{Message: fmt.Sprintf("%s cannot be read by the %s parser: %s.",
			shown(filename), refused.Parser, refused.Message)}, nil
	case parse.NotConfigured:
		// Choose refuses this first; it is answered here too so that a
		// caller that skipped Choose still gets the true reason.
		return &Refusal{Message: fmt.Sprintf(
			"The %s parser is not configured on this server. Choose another parser, or ask the administrator to configure it.",
			refused.Parser)}, nil
	}
	return nil, err
}

// shown is a file's name for a sentence, with a stand-in for an empty one.
func shown(filename string) string {
	if filename == "" {
		return "A file with no name"
	}
	return fmt.Sprintf("%q", filename)
}

// names is the four parser names, in picker order.
func names(registry Registry) []string {
	options := registry.Options()
	listed := make([]string, len(options))
	for index, option := range options {
		listed[index] = string(option.Parser)
	}
	return listed
}

// ReadDirectly is the extensions Go reads itself, split the way the pickers
// show them: text kept as it is, and tables and pages converted to Markdown.
func ReadDirectly() (plaintext, converted []string) {
	plaintext, converted = []string{}, []string{}
	for _, extension := range filekind.ReadInGo() {
		if filekind.Of(extension) == filekind.Text {
			plaintext = append(plaintext, extension)
		} else {
			converted = append(converted, extension)
		}
	}
	sort.Strings(plaintext)
	sort.Strings(converted)
	return plaintext, converted
}
