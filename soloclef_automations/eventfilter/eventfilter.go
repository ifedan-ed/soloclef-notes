// Package eventfilter decides whether an event's payload satisfies an event
// trigger's filter.
//
// A filter is a JSON object. Each key is a payload field and its value is
// either a plain value (the field must equal it) or an object of operators
// ($eq, $ne, $gt, $gte, $lt, $lte, $in, $nin), all of which must hold. The
// keys $and and $or take a list of filters and $not takes one. Sibling keys
// are all required. An empty filter matches every payload, and a field the
// payload does not carry fails every condition on it.
//
// An operator this package does not know is an error, never "no match": a
// misspelt filter is refused when the trigger is saved (Validate) instead
// of quietly never firing.
package eventfilter

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// Validate reports the first thing in filter that could never be evaluated.
func Validate(filter map[string]any) error {
	// Evaluating against an empty payload visits every key and operator
	// without needing one that matches.
	_, err := Matches(filter, map[string]any{})
	return err
}

// Matches reports whether payload satisfies filter.
func Matches(filter, payload map[string]any) (bool, error) {
	matched := true
	for key, value := range filter {
		ok, err := matchKey(key, value, payload)
		if err != nil {
			return false, err
		}
		// Every key is still visited after a miss, so that a bad operator
		// later in the filter is reported whatever the payload was.
		matched = matched && ok
	}
	return matched, nil
}

func matchKey(key string, value any, payload map[string]any) (bool, error) {
	switch {
	case key == "$and" || key == "$or":
		list, isList := value.([]any)
		if !isList {
			return false, fmt.Errorf("%s takes a list of filters", key)
		}
		every, some := true, false
		for _, entry := range list {
			sub, isFilter := entry.(map[string]any)
			if !isFilter {
				return false, fmt.Errorf("%s takes a list of filters", key)
			}
			ok, err := Matches(sub, payload)
			if err != nil {
				return false, err
			}
			every, some = every && ok, some || ok
		}
		if key == "$and" {
			return every, nil
		}
		return some, nil
	case key == "$not":
		sub, isFilter := value.(map[string]any)
		if !isFilter {
			return false, fmt.Errorf("$not takes a filter")
		}
		ok, err := Matches(sub, payload)
		return !ok, err
	case strings.HasPrefix(key, "$"):
		return false, fmt.Errorf("unknown operator %s", key)
	}
	actual, present := payload[key]
	operators, isOperators := value.(map[string]any)
	if !isOperators {
		return present && equal(actual, value), nil
	}
	matched := true
	for op, operand := range operators {
		ok, err := apply(op, actual, operand)
		if err != nil {
			return false, fmt.Errorf("field %s: %w", key, err)
		}
		matched = matched && ok && present
	}
	return matched, nil
}

func apply(op string, actual, operand any) (bool, error) {
	switch op {
	case "$eq":
		return equal(actual, operand), nil
	case "$ne":
		return !equal(actual, operand), nil
	case "$in", "$nin":
		list, isList := operand.([]any)
		if !isList {
			return false, fmt.Errorf("%s takes a list", op)
		}
		found := false
		for _, entry := range list {
			found = found || equal(actual, entry)
		}
		return found == (op == "$in"), nil
	case "$gt", "$gte", "$lt", "$lte":
		order, comparable := compare(actual, operand)
		if !comparable {
			// A number against a string, or a missing field: not greater,
			// not less.
			return false, nil
		}
		switch op {
		case "$gt":
			return order > 0, nil
		case "$gte":
			return order >= 0, nil
		case "$lt":
			return order < 0, nil
		}
		return order <= 0, nil
	}
	return false, fmt.Errorf("unknown operator %s", op)
}

// number reads a JSON number however it was decoded.
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	}
	return 0, false
}

// equal compares two JSON values; numbers compare by value whatever Go type
// each was decoded into.
func equal(a, b any) bool {
	if x, isNumber := number(a); isNumber {
		y, alsoNumber := number(b)
		return alsoNumber && x == y
	}
	return reflect.DeepEqual(a, b)
}

func compare(a, b any) (int, bool) {
	if x, isNumber := number(a); isNumber {
		y, alsoNumber := number(b)
		if !alsoNumber {
			return 0, false
		}
		switch {
		case x < y:
			return -1, true
		case x > y:
			return 1, true
		}
		return 0, true
	}
	x, isText := a.(string)
	y, alsoText := b.(string)
	if !isText || !alsoText {
		return 0, false
	}
	return strings.Compare(x, y), true
}
