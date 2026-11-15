package installedbuilds

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// A schema is one node of a snapshot's OpenAPI 3.1 document, as decoded
// from JSON. It stays a map rather than a struct because the documents were
// written by FastAPI and pydantic, not by this server, and a struct would
// quietly drop any keyword it did not name.
type schema = map[string]any

// mismatch is one way an answer differs from what a build was compiled
// against, at a JSON path ("items[0].created_at").
type mismatch struct {
	at, problem string
}

// missingProblem is a required field's absence.
const missingProblem = "is missing; the build requires it"

func (m mismatch) String() string {
	if m.at == "" {
		return m.problem
	}
	return m.at + ": " + m.problem
}

// compatibility checks one JSON value against a snapshot schema the way an
// installed build reads it:
//
//   - a field the schema lists as required must be there; the bundle checks
//     every answer against its openapi.json and refuses one without it;
//   - a value present must have a type the schema allows, null included
//     only where the schema says so;
//   - an enum or const value must be one the build knows.
//
// Extra fields are fine: a build ignores what it does not read. Formats,
// lengths and patterns are not checked; they constrain what a client sends,
// and no build re-validates an answer's string formats.
type compatibility struct {
	components map[string]any
	found      []mismatch
	// seen guards against a $ref cycle; one path through a cycle is enough.
	depth int
}

const maxDepth = 64

func (c *compatibility) check(at string, value any, node schema) {
	if node == nil || c.depth > maxDepth {
		return
	}
	c.depth++
	defer func() { c.depth-- }()

	if ref, ok := node["$ref"].(string); ok {
		target, err := c.resolve(ref)
		if err != nil {
			c.found = append(c.found, mismatch{at, err.Error()})
			return
		}
		c.check(at, value, target)
		return
	}
	// allOf: every part holds.
	if parts, ok := node["allOf"].([]any); ok {
		for _, part := range parts {
			c.check(at, value, asSchema(part))
		}
	}
	// anyOf / oneOf: some alternative holds. pydantic writes an optional
	// field as anyOf [X, null], so this is where null is allowed.
	for _, keyword := range []string{"anyOf", "oneOf"} {
		if alternatives, ok := node[keyword].([]any); ok {
			c.oneOf(at, value, alternatives)
		}
	}
	if constant, ok := node["const"]; ok && !sameJSON(constant, value) {
		c.found = append(c.found, mismatch{at, fmt.Sprintf("is %s, the build knows only %s", show(value), show(constant))})
		return
	}
	if allowed, ok := node["enum"].([]any); ok {
		known := false
		for _, one := range allowed {
			known = known || sameJSON(one, value)
		}
		if !known {
			c.found = append(c.found, mismatch{at, fmt.Sprintf("is %s, the build knows only %s", show(value), show(allowed))})
			return
		}
	}
	types := typesOf(node["type"])
	if len(types) == 0 {
		return
	}
	actual := jsonType(value)
	if !typeAllowed(actual, types) {
		c.found = append(c.found, mismatch{at, fmt.Sprintf("is %s, the build reads %s", describe(actual, value), strings.Join(types, " or "))})
		return
	}
	switch actual {
	case "object":
		c.object(at, value.(map[string]any), node)
	case "array":
		if items := asSchema(node["items"]); items != nil {
			for index, item := range value.([]any) {
				c.check(fmt.Sprintf("%s[%d]", at, index), item, items)
			}
		}
	}
}

func (c *compatibility) object(at string, value map[string]any, node schema) {
	properties, _ := node["properties"].(map[string]any)
	if required, ok := node["required"].([]any); ok {
		for _, name := range required {
			field, _ := name.(string)
			if _, present := value[field]; !present {
				c.found = append(c.found, mismatch{join(at, field), missingProblem})
			}
		}
	}
	names := make([]string, 0, len(value))
	for name := range value {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if property := asSchema(properties[name]); property != nil {
			c.check(join(at, name), value[name], property)
			continue
		}
		if extra := asSchema(node["additionalProperties"]); extra != nil {
			c.check(join(at, name), value[name], extra)
		}
	}
}

// oneOf records nothing when some alternative fits, and the closest
// alternative's mismatches when none does, so the message names a field
// rather than "matches none of 2 alternatives".
func (c *compatibility) oneOf(at string, value any, alternatives []any) {
	var closest []mismatch
	for _, alternative := range alternatives {
		trial := compatibility{components: c.components, depth: c.depth}
		trial.check(at, value, asSchema(alternative))
		if len(trial.found) == 0 {
			return
		}
		if closest == nil || len(trial.found) < len(closest) {
			closest = trial.found
		}
	}
	c.found = append(c.found, closest...)
}

func (c *compatibility) resolve(ref string) (schema, error) {
	const prefix = "#/components/schemas/"
	if !strings.HasPrefix(ref, prefix) {
		return nil, fmt.Errorf("the snapshot refers to %s, which is not a component schema", ref)
	}
	schemas, _ := c.components["schemas"].(map[string]any)
	target := asSchema(schemas[strings.TrimPrefix(ref, prefix)])
	if target == nil {
		return nil, fmt.Errorf("the snapshot refers to %s, which it does not define", ref)
	}
	return target, nil
}

func asSchema(value any) schema {
	node, _ := value.(map[string]any)
	return node
}

func typesOf(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []any:
		var out []string
		for _, one := range typed {
			if name, ok := one.(string); ok {
				out = append(out, name)
			}
		}
		return out
	}
	return nil
}

func jsonType(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case json.Number:
		if strings.ContainsAny(typed.String(), ".eE") {
			// 3.0 is a whole number to JSON Schema, and to a JavaScript client.
			if f, err := typed.Float64(); err == nil && f == float64(int64(f)) {
				return "integer"
			}
			return "number"
		}
		return "integer"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return fmt.Sprintf("%T", value)
}

func typeAllowed(actual string, allowed []string) bool {
	for _, name := range allowed {
		if name == actual || name == "number" && actual == "integer" {
			return true
		}
	}
	return false
}

func describe(actual string, value any) string {
	if actual == "null" {
		return "null"
	}
	return actual + " " + show(value)
}

func show(value any) string {
	encoded, _ := json.Marshal(value)
	if len(encoded) > 80 {
		return string(encoded[:77]) + "..."
	}
	return string(encoded)
}

func sameJSON(a, b any) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return bytes.Equal(left, right)
}

func join(at, field string) string {
	if at == "" {
		return field
	}
	return at + "." + field
}

// decode reads a body with numbers kept as written, so an integer the build
// expects is not mistaken for a float.
func decode(body []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}
