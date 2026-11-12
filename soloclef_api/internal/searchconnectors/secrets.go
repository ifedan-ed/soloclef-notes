package searchconnectors

import (
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// This file is app/mcp_security/secrets.py's read side: what any reader is
// shown of an MCP server's stored settings.
//
// # How the credential is stored, and what leaves
//
// An MCP server's key is stored **as it was typed**, in the `config` column
// (a `json` column), under `server_config.headers` -- or inside
// `server_config.url` when the provider wants it in the query string. It is
// not encrypted. What a reader gets is never that text: every header value,
// every `env` value and every key-like query value comes back as four bullets
// plus its last four characters, and as four bullets alone when it is shorter
// than twelve. Nothing here decrypts, encrypts or compares a credential, so
// there is no cipher for this tier to share; the mask is string work, and
// testdata/python_values.json holds Python's answer for each piece of it.

// mask is MASK.
const mask = "••••"

// maskValue is mask_value: the mask, and the last four characters of
// anything at least twelve long. The length is in code points, as len() of a
// Python str is, and a value that is not text is measured as str() of it.
func maskValue(value any) string {
	text := pyStr(value)
	if codePointLen(text) >= 12 {
		return mask + lastCodePoints(text, 4)
	}
	return mask
}

// sensitiveWords is _SENSITIVE_PARAM's alternatives: a query parameter whose
// name contains one of these, in any case, has its value masked.
var sensitiveWords = []string{"key", "token", "secret", "auth", "pass", "sig", "cred"}

// foldsToASCII is what makes "in any case" Python's and not Go's.
//
// The pattern is a str compiled with re.IGNORECASE, so its matching is
// Unicode-aware, and three letters of those seven words have non-ASCII
// spellings: U+017F "ſ" matches "s", U+212A (the Kelvin sign) matches "k",
// and U+0130 and U+0131 -- the dotted capital I and the dotless small one --
// both match "i". Go's `(?i)` knows the first two and not the last two, so a
// parameter named "sig" with a dotless i would be masked there and shown here.
// TestTheFoldingTableIsPythons sweeps every code point against each letter.
var foldsToASCII = map[rune]rune{
	0x017f: 's',
	0x212a: 'k',
	0x0130: 'i',
	0x0131: 'i',
}

// isSensitiveName is bool(_SENSITIVE_PARAM.search(name)).
func isSensitiveName(name string) bool {
	var folded strings.Builder
	for _, character := range name {
		switch {
		case character >= 'A' && character <= 'Z':
			folded.WriteRune(character + ('a' - 'A'))
		default:
			if ascii, special := foldsToASCII[character]; special {
				folded.WriteRune(ascii)
			} else {
				folded.WriteRune(character)
			}
		}
	}
	for _, word := range sensitiveWords {
		if strings.Contains(folded.String(), word) {
			return true
		}
	}
	return false
}

// maskAddress is _mask_url.
//
// An address with no query comes back untouched -- but only after urlsplit
// has had the chance to refuse it. One with a query is rebuilt: every pair is
// decoded and encoded again, so `a%20b` becomes `a+b` and a bare `flag`
// becomes `flag=` whether or not anything in it was masked.
func maskAddress(address string) (string, error) {
	parts, err := urlSplit(address)
	if err != nil {
		return "", err
	}
	if parts.query == "" {
		return address, nil
	}
	pairs := parseQuery(parts.query)
	for index, pair := range pairs {
		if isSensitiveName(pair.name) {
			pairs[index].value = maskValue(pair.value)
		}
	}
	query, err := encodeQuery(pairs)
	if err != nil {
		return "", err
	}
	parts.query = query
	return urlUnsplit(parts), nil
}

// maskServerConfig is mask_server_config: a copy with `headers` and `env`
// masked value by value when they are objects, and `url` masked when it is
// text. A key of any other type is passed through as it is -- a list where
// the headers should be is returned as that list.
func maskServerConfig(server *pythonjson.Object) (*pythonjson.Object, error) {
	masked := pythonjson.NewObject()
	for _, key := range server.Keys() {
		value, _ := server.Get(key)
		masked.Set(key, value)
	}
	for _, key := range []string{"headers", "env"} {
		if values, isObject := server.GetOrNil(key).(*pythonjson.Object); isObject {
			hidden := pythonjson.NewObject()
			for _, name := range values.Keys() {
				value, _ := values.Get(name)
				hidden.Set(name, maskValue(value))
			}
			masked.Set(key, hidden)
		}
	}
	if address, isText := server.GetOrNil("url").(string); isText {
		hidden, err := maskAddress(address)
		if err != nil {
			return nil, err
		}
		masked.Set("url", hidden)
	}
	return masked, nil
}

// maskConnectorConfig is mask_connector_config for a config that is an
// object: the same object with its `server_config` masked when that is an
// object too.
func maskConnectorConfig(config *pythonjson.Object) (*pythonjson.Object, error) {
	masked := pythonjson.NewObject()
	for _, key := range config.Keys() {
		value, _ := config.Get(key)
		masked.Set(key, value)
	}
	if server, isObject := config.GetOrNil("server_config").(*pythonjson.Object); isObject {
		hidden, err := maskServerConfig(server)
		if err != nil {
			return nil, err
		}
		masked.Set("server_config", hidden)
	}
	return masked, nil
}
