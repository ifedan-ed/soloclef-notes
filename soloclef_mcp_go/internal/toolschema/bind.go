package toolschema

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/pythonjson"
)

// Arguments is one call's parameters after binding: every declared name present,
// holding either the client's value or the parameter's default.
type Arguments struct {
	values map[string]any
}

// Text reads a string parameter; a nullable one that was null reads as "".
func (a Arguments) Text(name string) string {
	text, _ := a.values[name].(string)
	return text
}

// OptionalText reads a nullable string parameter and whether it was given.
func (a Arguments) OptionalText(name string) (string, bool) {
	text, ok := a.values[name].(string)
	return text, ok
}

// Number reads an integer parameter.
func (a Arguments) Number(name string) int {
	number, _ := a.values[name].(int)
	return number
}

// OptionalNumber reads a nullable integer parameter and whether it was given.
func (a Arguments) OptionalNumber(name string) (int, bool) {
	number, ok := a.values[name].(int)
	return number, ok
}

// Flag reads a boolean parameter.
func (a Arguments) Flag(name string) bool {
	flag, _ := a.values[name].(bool)
	return flag
}

// TextList reads a string-array parameter; a nullable one that was null reads as nil.
func (a Arguments) TextList(name string) []string {
	list, _ := a.values[name].([]string)
	return list
}

// Bind checks a call's arguments against the signature and returns them with
// defaults applied, or the refusal pydantic would have produced.
//
// Faults are collected for every parameter rather than returned at the first,
// because pydantic reports them all and the count is in its first line.
func (s Signature) Bind(raw json.RawMessage) (Arguments, error) {
	given, err := pythonjson.DecodeObject(raw)
	if err != nil {
		return Arguments{}, err
	}
	bound := Arguments{values: make(map[string]any, len(s.Parameters))}
	report := &ValidationReport{ArgumentsTitle: s.ArgumentsTitle}
	for _, parameter := range s.Parameters {
		value, present := given.Lookup(parameter.Name)
		if !present {
			if parameter.Required {
				report.Faults = append(report.Faults, Fault{
					Location:   parameter.Name,
					Message:    "Field required",
					Type:       "missing",
					InputValue: pythonjson.Truncated(given),
					InputType:  pythonjson.TypeName(given),
				})
				continue
			}
			if parameter.Default != nil {
				bound.values[parameter.Name] = parameter.Default
			}
			continue
		}
		accepted, faults := parameter.accept(value)
		if len(faults) > 0 {
			report.Faults = append(report.Faults, faults...)
			continue
		}
		if accepted != nil {
			bound.values[parameter.Name] = accepted
		}
	}
	if len(report.Faults) > 0 {
		return Arguments{}, report
	}
	return bound, nil
}

// accept coerces one given value, then checks the parameter's constraints.
//
// A value that failed coercion is not then measured against a minimum, because
// pydantic reports one fault per field and stops at the type.
func (p Parameter) accept(value pythonjson.Value) (any, []Fault) {
	if p.Nullable && value.Kind == pythonjson.Null {
		return nil, nil
	}
	accepted, fault := p.coerce(value)
	if fault != nil {
		return nil, []Fault{*fault}
	}
	if faults := p.constrain(accepted, value); len(faults) > 0 {
		return nil, faults
	}
	return accepted, nil
}

func (p Parameter) coerce(value pythonjson.Value) (any, *Fault) {
	switch p.Kind {
	case Number:
		return p.coerceNumber(value)
	case Flag:
		return p.coerceFlag(value)
	case TextList:
		return p.coerceTextList(value)
	default:
		return p.coerceText(value)
	}
}

func (p Parameter) fault(value pythonjson.Value, message, kind string) *Fault {
	return &Fault{
		Location:   p.Name,
		Message:    message,
		Type:       kind,
		InputValue: pythonjson.Truncated(value),
		InputType:  pythonjson.TypeName(value),
	}
}

func (p Parameter) coerceText(value pythonjson.Value) (any, *Fault) {
	if len(p.Choices) > 0 {
		if value.Kind == pythonjson.String {
			for _, choice := range p.Choices {
				if value.String == choice {
					return value.String, nil
				}
			}
		}
		return nil, p.fault(value,
			"Input should be "+choiceList(p.Choices), "literal_error")
	}
	if value.Kind != pythonjson.String {
		return nil, p.fault(value, "Input should be a valid string", "string_type")
	}
	return value.String, nil
}

func (p Parameter) coerceNumber(value pythonjson.Value) (any, *Fault) {
	switch value.Kind {
	case pythonjson.Int:
		return int(value.Int), nil
	case pythonjson.Bool:
		if value.Bool {
			return 1, nil
		}
		return 0, nil
	case pythonjson.Float:
		if value.Float != math.Trunc(value.Float) {
			return nil, p.fault(value,
				"Input should be a valid integer, got a number with a fractional part",
				"int_from_float")
		}
		return int(value.Float), nil
	case pythonjson.String:
		number, ok := parseNumericText(value.String)
		if !ok {
			return nil, p.fault(value,
				"Input should be a valid integer, unable to parse string as an integer",
				"int_parsing")
		}
		return number, nil
	default:
		return nil, p.fault(value, "Input should be a valid integer", "int_type")
	}
}

// parseNumericText mirrors pydantic's lax string-to-integer step: surrounding
// whitespace ignored, Python's int() syntax including underscore separators, and
// a decimal spelling whose fractional part is zero ("3.0"). Anything else is not
// an integer at all, which is why "2.5" is a parse failure here rather than a
// fractional-part refusal.
//
// The trimming is load-bearing: a client that pads a number — " 3 " — is
// accepted by the Python server, and refusing it here would be a refusal that
// server never gave.
func parseNumericText(text string) (int, bool) {
	cleaned := strings.ReplaceAll(strings.TrimSpace(text), "_", "")
	if integer, err := strconv.ParseInt(cleaned, 10, 64); err == nil {
		return int(integer), true
	}
	number, err := strconv.ParseFloat(cleaned, 64)
	if err != nil || number != math.Trunc(number) {
		return 0, false
	}
	return int(number), true
}

// flagWords are the strings pydantic reads as booleans, matched without regard
// to case but without trimming: " true " is not one of them.
var flagWords = map[string]bool{
	"0": false, "off": false, "f": false, "false": false, "n": false, "no": false,
	"1": true, "on": true, "t": true, "true": true, "y": true, "yes": true,
}

func (p Parameter) coerceFlag(value pythonjson.Value) (any, *Fault) {
	switch value.Kind {
	case pythonjson.Bool:
		return value.Bool, nil
	case pythonjson.Int:
		if value.Int == 0 || value.Int == 1 {
			return value.Int == 1, nil
		}
		return nil, p.unreadableFlag(value)
	case pythonjson.Float:
		if value.Float == 0 || value.Float == 1 {
			return value.Float == 1, nil
		}
		return nil, p.fault(value, "Input should be a valid boolean", "bool_type")
	case pythonjson.String:
		if flag, ok := flagWords[strings.ToLower(value.String)]; ok {
			return flag, nil
		}
		return nil, p.unreadableFlag(value)
	default:
		return nil, p.fault(value, "Input should be a valid boolean", "bool_type")
	}
}

func (p Parameter) unreadableFlag(value pythonjson.Value) *Fault {
	return p.fault(value,
		"Input should be a valid boolean, unable to interpret input", "bool_parsing")
}

func (p Parameter) coerceTextList(value pythonjson.Value) (any, *Fault) {
	if value.Kind != pythonjson.Array {
		return nil, p.fault(value, "Input should be a valid list", "list_type")
	}
	list := make([]string, 0, len(value.Array))
	for index, element := range value.Array {
		if element.Kind != pythonjson.String {
			return nil, &Fault{
				Location:   fmt.Sprintf("%s.%d", p.Name, index),
				Message:    "Input should be a valid string",
				Type:       "string_type",
				InputValue: pythonjson.Truncated(element),
				InputType:  pythonjson.TypeName(element),
			}
		}
		list = append(list, element.String)
	}
	return list, nil
}

func (p Parameter) constrain(accepted any, given pythonjson.Value) []Fault {
	var faults []Fault
	switch typed := accepted.(type) {
	case int:
		if p.Minimum != nil && typed < *p.Minimum {
			faults = append(faults, *p.fault(given,
				fmt.Sprintf("Input should be greater than or equal to %d", *p.Minimum),
				"greater_than_equal"))
		}
		if p.Maximum != nil && typed > *p.Maximum {
			faults = append(faults, *p.fault(given,
				fmt.Sprintf("Input should be less than or equal to %d", *p.Maximum),
				"less_than_equal"))
		}
	case string:
		if p.MinLength != nil && len([]rune(typed)) < *p.MinLength {
			faults = append(faults, *p.fault(given,
				fmt.Sprintf("String should have at least %d %s",
					*p.MinLength, plural("character", *p.MinLength)),
				"string_too_short"))
		}
	case []string:
		if p.MinItems != nil && len(typed) < *p.MinItems {
			faults = append(faults, *p.fault(given,
				fmt.Sprintf("List should have at least %d %s after validation, not %d",
					*p.MinItems, plural("item", *p.MinItems), len(typed)),
				"too_short"))
		}
	}
	return faults
}
