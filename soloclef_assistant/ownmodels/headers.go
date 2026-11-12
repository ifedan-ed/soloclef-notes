package ownmodels

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model/openaicompat"
)

// A connection's own headers: what a person may send beside the key, how
// they are kept, and how they are read back for a call.
//
// They are stored in the connection's `extra`, sealed whole under the
// account's key seal (`headers_sealed`), with their names beside them in
// the clear (`header_names`) so the settings screen can show which headers
// a connection sends without being told what they say. A header can carry a
// credential exactly as the key does (an `api-key`, a gateway's token), so
// its value gets the key's treatment: sealed at rest, never answered.

// The two keys of `extra` the headers live under. ExtraKeys lists them for
// the code that must keep a person's `extra` from naming them.
const (
	extraSealed = "headers_sealed"
	extraNames  = "header_names"
)

// ExtraKeys are the keys of a connection's `extra` that hold its headers.
// A body's `extra` may not name them (the headers are set through their own
// field), and an answer leaves them out.
var ExtraKeys = []string{extraSealed, extraNames}

// The bounds of a connection's headers. A gateway asks for one or two; the
// bounds exist so that a request cannot store a megabyte of them.
const (
	maxHeaders         = 20
	maxHeaderNameChars = 100
	maxHeaderValue     = 4096
)

// CheckHeaders says, in a sentence for the person at the form, why a set of
// headers cannot be used, or "" when it can. The rule for a header this
// server sets itself is openaicompat's, so the form refuses exactly what a
// call would.
func CheckHeaders(headers map[string]string) string {
	if len(headers) > maxHeaders {
		return fmt.Sprintf("A connection can send at most %d headers", maxHeaders)
	}
	seen := make(map[string]bool, len(headers))
	for name, value := range headers {
		trimmed := strings.TrimSpace(name)
		switch {
		case trimmed == "" || utf8.RuneCountInString(trimmed) > maxHeaderNameChars || !isToken(trimmed):
			return "Each header needs a name made of letters, digits and dashes"
		case openaicompat.OwnsHeader(trimmed):
			return "The " + http.CanonicalHeaderKey(trimmed) + " header is set by Soloclef: put the key in the key field"
		case seen[http.CanonicalHeaderKey(trimmed)]:
			return "The " + http.CanonicalHeaderKey(trimmed) + " header is given twice"
		case len(value) > maxHeaderValue || !utf8.ValidString(value) || strings.ContainsAny(value, "\r\n\x00"):
			return "The value of the " + http.CanonicalHeaderKey(trimmed) + " header is too long or has a line break"
		}
		seen[http.CanonicalHeaderKey(trimmed)] = true
	}
	return ""
}

// isToken reports whether a header name is an RFC 9110 token.
func isToken(name string) bool {
	for _, character := range name {
		switch {
		case character >= 'a' && character <= 'z', character >= 'A' && character <= 'Z', character >= '0' && character <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", character):
		default:
			return false
		}
	}
	return true
}

// SealedHeaders is a connection's headers as `extra` keeps them: the JSON
// object to merge into `extra`. No headers is the object that removes them
// (both keys absent), which the store writes as a removal.
//
// Call CheckHeaders first; this does not.
func SealedHeaders(seal KeySeal, userID string, headers map[string]string) (json.RawMessage, error) {
	if len(headers) == 0 {
		return json.RawMessage(`{}`), nil
	}
	canonical := make(map[string]string, len(headers))
	names := make([]string, 0, len(headers))
	for name, value := range headers {
		key := http.CanonicalHeaderKey(strings.TrimSpace(name))
		canonical[key] = value
		names = append(names, key)
	}
	sort.Strings(names)
	plain, err := json.Marshal(canonical)
	if err != nil {
		return nil, err
	}
	sealed, err := seal.Seal(userID, string(plain))
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{extraSealed: sealed, extraNames: names})
}

// OpenHeaders is the headers a connection's sealed text holds. "" is none.
func OpenHeaders(seal KeySeal, userID, sealed string) (map[string]string, error) {
	if sealed == "" {
		return nil, nil
	}
	plain, err := seal.Open(userID, sealed)
	if err != nil {
		return nil, err
	}
	var headers map[string]string
	if err := json.Unmarshal([]byte(plain), &headers); err != nil {
		// Sealed by this code, so unreadable JSON is damage, cured as an
		// unreadable key is.
		return nil, ErrSealUnreadable
	}
	return headers, nil
}

// SealedHeadersOf is the sealed text in a connection's `extra`, or "".
func SealedHeadersOf(extra []byte) string {
	var fields struct {
		Sealed string `json:"headers_sealed"`
	}
	if json.Unmarshal(extra, &fields) != nil {
		return ""
	}
	return fields.Sealed
}

// HeaderNamesOf is the header names in a connection's `extra`, sorted, or
// an empty list.
func HeaderNamesOf(extra []byte) []string {
	var fields struct {
		Names []string `json:"header_names"`
	}
	if json.Unmarshal(extra, &fields) != nil || fields.Names == nil {
		return []string{}
	}
	return fields.Names
}
