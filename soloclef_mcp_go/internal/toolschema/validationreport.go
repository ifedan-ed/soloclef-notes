package toolschema

import (
	"fmt"
	"strings"
)

// pydanticVersion appears in every refusal's "for further information" line, so
// it is written once here and named for what it is: the version of the library
// whose messages these mirror.
const pydanticVersion = "2.13"

// Fault is one rejected parameter, in pydantic's terms.
type Fault struct {
	// Location is the field path, e.g. "start_urls" or "start_urls.0".
	Location string
	// Message is the sentence pydantic puts first, e.g. "Field required".
	Message string
	// Type is pydantic's machine name for the fault, e.g. "int_parsing"; it is
	// printed twice, in the bracket and in the documentation link.
	Type string
	// InputValue is the offending value as Python's repr prints it, already
	// truncated.
	InputValue string
	// InputType is the Python type name of the offending value.
	InputType string
}

// ValidationReport is every fault found in one call's arguments.
//
// The Python server raises pydantic's ValidationError and the transport prints
// it, so a port that refuses in different words tells the model something else.
// Report() reproduces that printing.
type ValidationReport struct {
	ArgumentsTitle string
	Faults         []Fault
}

func (r *ValidationReport) Error() string { return r.Report() }

// Report renders the refusal exactly as pydantic prints a ValidationError.
func (r *ValidationReport) Report() string {
	noun := "validation errors"
	if len(r.Faults) == 1 {
		noun = "validation error"
	}
	lines := []string{fmt.Sprintf("%d %s for %s", len(r.Faults), noun, r.ArgumentsTitle)}
	for _, fault := range r.Faults {
		lines = append(lines,
			fault.Location,
			fmt.Sprintf("  %s [type=%s, input_value=%s, input_type=%s]",
				fault.Message, fault.Type, fault.InputValue, fault.InputType),
			fmt.Sprintf("    For further information visit https://errors.pydantic.dev/%s/v/%s",
				pydanticVersion, fault.Type),
		)
	}
	return strings.Join(lines, "\n")
}

// choiceList renders a Literal's members as pydantic lists them: comma
// separated, with "or" before the last.
func choiceList(choices []string) string {
	quoted := make([]string, 0, len(choices))
	for _, choice := range choices {
		quoted = append(quoted, "'"+choice+"'")
	}
	switch len(quoted) {
	case 0:
		return ""
	case 1:
		return quoted[0]
	default:
		return strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
	}
}

// plural returns the noun with an "s" unless the count is one, which is how
// pydantic writes "1 character" and "2 characters".
func plural(noun string, count int) string {
	if count == 1 {
		return noun
	}
	return noun + "s"
}
