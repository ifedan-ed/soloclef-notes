package toolschema

import (
	"bytes"
	"encoding/json"
)

// JSONSchema is the "inputSchema" a client reads from tools/list.
//
// Members are written through Go maps, whose marshalling sorts keys, because
// the Python server's schema is sorted too: the two then agree byte for byte
// rather than only after canonicalisation.
func (s Signature) JSONSchema() (json.RawMessage, error) {
	properties := make(map[string]any, len(s.Parameters))
	var required []string
	for _, parameter := range s.Parameters {
		properties[parameter.Name] = parameter.schema()
		if parameter.Required {
			required = append(required, parameter.Name)
		}
	}
	schema := map[string]any{
		"properties": properties,
		"title":      s.ArgumentsTitle,
		"type":       "object",
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return marshalUnescaped(schema)
}

func marshalUnescaped(value any) (json.RawMessage, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return json.RawMessage(bytes.TrimRight(out.Bytes(), "\n")), nil
}

func (p Parameter) schema() map[string]any {
	property := map[string]any{"title": p.Title}
	if p.Description != "" {
		property["description"] = p.Description
	}
	if p.Nullable {
		property["anyOf"] = []any{p.typeSchema(), map[string]any{"type": "null"}}
		property["default"] = nil
		return property
	}
	for key, value := range p.typeSchema() {
		property[key] = value
	}
	for key, value := range p.constraintSchema() {
		property[key] = value
	}
	if !p.Required {
		property["default"] = p.Default
	}
	return property
}

// typeSchema is the part of the schema that says what the value is, which for a
// nullable parameter is one branch of an anyOf and so has to stand alone.
func (p Parameter) typeSchema() map[string]any {
	switch p.Kind {
	case Number:
		return map[string]any{"type": "integer"}
	case Flag:
		return map[string]any{"type": "boolean"}
	case TextList:
		return map[string]any{"items": map[string]any{"type": "string"}, "type": "array"}
	default:
		if len(p.Choices) > 0 {
			return map[string]any{"enum": p.Choices, "type": "string"}
		}
		return map[string]any{"type": "string"}
	}
}

func (p Parameter) constraintSchema() map[string]any {
	constraints := map[string]any{}
	if p.Minimum != nil {
		constraints["minimum"] = *p.Minimum
	}
	if p.Maximum != nil {
		constraints["maximum"] = *p.Maximum
	}
	if p.MinLength != nil {
		constraints["minLength"] = *p.MinLength
	}
	if p.MinItems != nil {
		constraints["minItems"] = *p.MinItems
	}
	return constraints
}
