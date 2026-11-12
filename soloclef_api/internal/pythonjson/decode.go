package pythonjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Decode reads a JSON document into this package's value model, keeping object
// keys in document order.
//
// Order matters because Python's json.loads builds a dict in document order and
// json.dumps writes it back in that order, so a value read out of a json column
// and written into a response keeps the order it was stored in. A Go map would
// lose it.
func Decode(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("pythonjson: trailing data after JSON value")
	}
	return value, nil
}

func decodeValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	return decodeFrom(decoder, token)
}

func decodeFrom(decoder *json.Decoder, token json.Token) (any, error) {
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			return decodeObject(decoder)
		case '[':
			return decodeArray(decoder)
		default:
			return nil, fmt.Errorf("pythonjson: unexpected %q", value)
		}
	case json.Number:
		return decodeNumber(value)
	default:
		// string, bool and nil already match the value model.
		return value, nil
	}
}

func decodeObject(decoder *json.Decoder) (any, error) {
	object := NewObject()
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		if delim, isDelim := token.(json.Delim); isDelim && delim == '}' {
			return object, nil
		}
		key, isString := token.(string)
		if !isString {
			return nil, fmt.Errorf("pythonjson: object key is not a string")
		}
		value, err := decodeValue(decoder)
		if err != nil {
			return nil, err
		}
		object.Set(key, value)
	}
}

func decodeArray(decoder *json.Decoder) (any, error) {
	items := []any{}
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		if delim, isDelim := token.(json.Delim); isDelim && delim == ']' {
			return items, nil
		}
		item, err := decodeFrom(decoder, token)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
}

// decodeNumber splits integers from floats the way json.loads does: integer
// text becomes an exact integer, anything with a point or an exponent becomes a
// float and is later written as Python's repr of that float.
func decodeNumber(number json.Number) (any, error) {
	text := number.String()
	if !strings.ContainsAny(text, ".eE") {
		return RawInt(text), nil
	}
	parsed, err := strconv.ParseFloat(text, 64)
	if errors.Is(err, strconv.ErrRange) {
		// Too large for a float64. json.loads does not fail on it: float()
		// answers inf, the value is held like any other, and only *writing*
		// it fails, because json.dumps is called with allow_nan=False. So a
		// column holding such a number somewhere a route never renders is
		// still read there, and refusing it here would be a 500 where the
		// Python tier answers 200. ParseFloat hands back the infinity along
		// with the range error, and Marshal refuses it exactly where
		// json.dumps does. (A value too *small* is not an error at all: both
		// languages round it to zero.)
		return parsed, nil
	}
	if err != nil {
		return nil, fmt.Errorf("pythonjson: %q is not a number: %w", text, err)
	}
	return parsed, nil
}
