// Package appversion reads the phone app's version numbers ("1.10.0") and
// says which of two is newer. The API checks a registering phone's version
// with it and the worker compares that with the App Store's, so a version
// the one accepts is always one the other can compare.
//
// A text comparison gets this wrong ("1.10.0" < "1.9.0" as strings), and a
// wrong answer here pushes "an update is out" to someone on the newest
// version. So each dot-separated part is a whole number, a missing part
// counts as 0 ("1.1" is "1.1.0": Apple's lookup writes "1.0" for what the
// app's config calls "1.0.0"), and anything else (a letter, a pre-release
// tag, an empty part, a sign) is not a version: it is refused, never read
// as something close. App Store Connect allows only plain dotted numbers,
// so a refusal means a malformed answer, not a real release.
//
// The shape is the one push_devices.app_version and
// app_store_versions.version are checked against (migration 00022): one to
// four parts of one to nine digits, so every part fits an int with room to
// spare and a stored version always parses.
package appversion

import (
	"errors"
	"strings"
)

// Version is a parsed version: its parts, most significant first.
type Version struct {
	parts []int
	text  string
}

// The shape's limits (migration 00022 checks the same).
const (
	maxParts  = 4
	maxDigits = 9
)

// ErrShape is a version that is not a plain dotted number.
var ErrShape = errors.New(`a version is one to four whole numbers separated by dots, such as "1.2.0"`)

// Parse reads a version. Surrounding whitespace is not trimmed: a stored or
// sent version is exactly the text it is, and " 1.2" is not one.
func Parse(text string) (Version, error) {
	if text == "" {
		return Version{}, ErrShape
	}
	fields := strings.Split(text, ".")
	if len(fields) > maxParts {
		return Version{}, ErrShape
	}
	parts := make([]int, len(fields))
	for i, field := range fields {
		if field == "" || len(field) > maxDigits {
			return Version{}, ErrShape
		}
		value := 0
		for j := 0; j < len(field); j++ {
			c := field[j]
			if c < '0' || c > '9' {
				return Version{}, ErrShape
			}
			value = value*10 + int(c-'0')
		}
		parts[i] = value
	}
	return Version{parts: parts, text: text}, nil
}

// String is the version as it was written, so what is stored and shown is
// what the phone or Apple said ("1.0", not a rewritten "1.0.0").
func (v Version) String() string { return v.text }

// Compare is negative when v is older than other, positive when newer, and
// 0 when they are the same release ("1.2" and "1.2.0" are).
func (v Version) Compare(other Version) int {
	for i := 0; i < max(len(v.parts), len(other.parts)); i++ {
		a, b := part(v.parts, i), part(other.parts, i)
		switch {
		case a < b:
			return -1
		case a > b:
			return 1
		}
	}
	return 0
}

// part is the i-th part, or 0 past the end: a missing part is a zero.
func part(parts []int, i int) int {
	if i < len(parts) {
		return parts[i]
	}
	return 0
}

// Older reports whether v is a release before other.
func (v Version) Older(other Version) bool { return v.Compare(other) < 0 }
