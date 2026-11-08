// Package orderedjson decodes and re-encodes JSON while remembering each
// object's member order and whether a number was written with a fraction.
//
// Both are visible to a caller. A rejected argument is reported with Python's
// repr of the decoded value, which prints members in insertion order and tells
// 3 from 3.0; and a tool that renders a backend reply as JSON reproduces
// Python's json.dumps of the same reply, which also keeps member order. Go's
// own decoding into a map loses both.
package pythonjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Kind names what a decoded JSON value is, in the terms Python would use.
type Kind int

const (
	Null Kind = iota
	Bool
	Int
	Float
	String
	Array
	Object
)

// Value is one decoded JSON value.
type Value struct {
	Kind    Kind
	Bool    bool
	Int     int64
	Float   float64
	String  string
	Array   []Value
	Members []Member
}

// Member is one key and value of an object, at the position the client wrote it.
type Member struct {
	Key   string
	Value Value
}

// Lookup returns the member with this key, and whether it was present.
func (v Value) Lookup(key string) (Value, bool) {
	for _, member := range v.Members {
		if member.Key == key {
			return member.Value, true
		}
	}
	return Value{}, false
}

// EmptyObject is an object with no members.
func EmptyObject() Value { return Value{Kind: Object} }

// DecodeObject reads an arguments object, keeping member order and number
// spelling.
//
// A JSON-RPC "arguments" field that is absent or null is an empty object, which
// is how the Python server's own transport presents it.
func DecodeObject(raw json.RawMessage) (Value, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return EmptyObject(), nil
	}
	value, err := DecodeAny(raw)
	if err != nil {
		return Value{}, err
	}
	if value.Kind == Null {
		return EmptyObject(), nil
	}
	if value.Kind != Object {
		return Value{}, fmt.Errorf("arguments must be a JSON object")
	}
	return value, nil
}

// DecodeAny reads any JSON value, keeping member order and number spelling.
func DecodeAny(raw []byte) (Value, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return Value{Kind: Null}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decodeValue(decoder)
}

func decodeValue(decoder *json.Decoder) (Value, error) {
	token, err := decoder.Token()
	if err != nil {
		return Value{}, err
	}
	return decodeFromToken(decoder, token)
}

func decodeFromToken(decoder *json.Decoder, token json.Token) (Value, error) {
	switch typed := token.(type) {
	case nil:
		return Value{Kind: Null}, nil
	case bool:
		return Value{Kind: Bool, Bool: typed}, nil
	case string:
		return Value{Kind: String, String: typed}, nil
	case json.Number:
		return decodeNumber(typed)
	case json.Delim:
		switch typed {
		case '[':
			return decodeArray(decoder)
		case '{':
			return decodeObject(decoder)
		}
	}
	return Value{}, fmt.Errorf("unexpected JSON token %v", token)
}

func decodeNumber(number json.Number) (Value, error) {
	text := number.String()
	if !strings.ContainsAny(text, ".eE") {
		parsed, err := number.Int64()
		if err == nil {
			return Value{Kind: Int, Int: parsed}, nil
		}
	}
	parsed, err := number.Float64()
	if err != nil {
		return Value{}, err
	}
	return Value{Kind: Float, Float: parsed}, nil
}

func decodeArray(decoder *json.Decoder) (Value, error) {
	value := Value{Kind: Array, Array: []Value{}}
	for {
		token, err := decoder.Token()
		if err != nil {
			return Value{}, err
		}
		if delim, ok := token.(json.Delim); ok && delim == ']' {
			return value, nil
		}
		element, err := decodeFromToken(decoder, token)
		if err != nil {
			return Value{}, err
		}
		value.Array = append(value.Array, element)
	}
}

func decodeObject(decoder *json.Decoder) (Value, error) {
	value := Value{Kind: Object}
	for {
		token, err := decoder.Token()
		if err != nil {
			return Value{}, err
		}
		if delim, ok := token.(json.Delim); ok && delim == '}' {
			return value, nil
		}
		key, ok := token.(string)
		if !ok {
			return Value{}, fmt.Errorf("object key is not a string: %v", token)
		}
		member, err := decodeValue(decoder)
		if err != nil {
			if err == io.EOF {
				return Value{}, fmt.Errorf("object member %q has no value", key)
			}
			return Value{}, err
		}
		value.Members = append(value.Members, Member{Key: key, Value: member})
	}
}
