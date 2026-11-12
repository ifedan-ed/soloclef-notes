package objecthttp

import "strings"

// NoneMatch reports whether an If-None-Match header names the entity tag,
// which is the question "does the client already hold exactly this?".
//
// The comparison is the weak one RFC 9110 section 13.1.2 prescribes for this
// header: a `W/` prefix on either side is ignored, the header may list
// several tags, and `*` matches anything that exists. The Python route
// compared the whole header to the tag with `==`, so a proxy that weakened
// the tag (as compressing proxies do) or a client holding two versions never
// got its 304 and re-downloaded the file every time.
//
// It is exported because a route that answers from rows rather than from the
// store -- the artifact manifest -- asks the same question of its own tag.
func NoneMatch(header, etag string) bool {
	if etag == "" {
		return false
	}
	want := opaque(etag)
	for _, candidate := range tags(header) {
		if candidate == "*" || opaque(candidate) == want {
			return true
		}
	}
	return false
}

// rangeStillValid is If-Range: "send me the range only if the object is
// still the one I have part of; otherwise send all of it". With no If-Range
// header the range stands.
//
// The comparison here is the strong one (RFC 9110 section 13.1.5): a weak
// tag never matches, because bytes from two merely-equivalent versions must
// not be spliced together. The header's other form, a date, is not compared
// at all and so never validates -- the safe direction, since the cost of a
// wrong "no" is one full download and the cost of a wrong "yes" is a
// corrupt file.
func rangeStillValid(header, etag string) bool {
	header = strings.TrimSpace(header)
	if header == "" {
		return true
	}
	return etag != "" && !strings.HasPrefix(etag, "W/") && header == etag
}

// opaque is a tag without its weakness marker.
func opaque(tag string) string { return strings.TrimPrefix(tag, "W/") }

// tags splits a header's list of entity tags. A tag is a quoted string that
// may itself contain a comma, so the list is walked quote by quote rather
// than split on commas.
func tags(header string) []string {
	var found []string
	rest := strings.TrimSpace(header)
	for rest != "" {
		if rest[0] == '*' {
			found = append(found, "*")
			rest = rest[1:]
		} else {
			start := 0
			if strings.HasPrefix(rest, "W/") {
				start = 2
			}
			if start >= len(rest) || rest[start] != '"' {
				// Not a tag. Nothing after it can be trusted to be one.
				return found
			}
			closing := strings.IndexByte(rest[start+1:], '"')
			if closing < 0 {
				return found
			}
			end := start + 1 + closing + 1
			found = append(found, rest[:end])
			rest = rest[end:]
		}
		rest = strings.TrimLeft(rest, " \t")
		if !strings.HasPrefix(rest, ",") {
			return found
		}
		rest = strings.TrimLeft(rest[1:], " \t")
	}
	return found
}
