package requestbody

// carriesUnpairedSurrogate reports whether the body holds a \uD800–\uDFFF
// escape that is not half of a valid pair.
//
// Go's encoding/json replaces such an escape with U+FFFD and reads on, so this
// tier would answer 200 for a body the Python tier cannot finish: pydantic-core
// carries the lone surrogate into a Python str, the route writes the row, and
// then the *response* cannot be encoded — a 500 with the row written. Asked of
// the running tier with `{"log_metadata":{"a":"\ud800"}}` and with a lone
// surrogate in the message: 500 both times, and the row there afterwards.
//
// Nothing here can reproduce that, because what Go hands the route has already
// lost the surrogate. So the body is ErrUndecodable and the request is
// forwarded, which gives the identical answer *and* the identical row, because
// the Python tier does the write itself.
func carriesUnpairedSurrogate(body []byte) bool {
	for index := 0; index < len(body); index++ {
		if body[index] != '\\' {
			continue
		}
		// A run of backslashes escapes itself in pairs; only an odd run leaves
		// the next character escaped.
		run := 1
		for index+run < len(body) && body[index+run] == '\\' {
			run++
		}
		if run%2 == 0 {
			index += run - 1
			continue
		}
		index += run - 1
		code, ok := escapedCodeUnit(body, index+1)
		if !ok {
			continue
		}
		index += 5
		if code >= 0xdc00 && code <= 0xdfff {
			return true // a low surrogate with no high one before it
		}
		if code < 0xd800 || code > 0xdbff {
			continue
		}
		// A high surrogate must be followed immediately by a low one.
		if index+1 >= len(body) || body[index+1] != '\\' {
			return true
		}
		low, ok := escapedCodeUnit(body, index+2)
		if !ok || low < 0xdc00 || low > 0xdfff {
			return true
		}
		index += 6
	}
	return false
}

// escapedCodeUnit reads a \uXXXX escape whose backslash is just before start.
func escapedCodeUnit(body []byte, start int) (int, bool) {
	if start >= len(body) || body[start] != 'u' || start+4 >= len(body) {
		return 0, false
	}
	code := 0
	for offset := 1; offset <= 4; offset++ {
		digit, ok := hexDigit(body[start+offset])
		if !ok {
			return 0, false
		}
		code = code*16 + digit
	}
	return code, true
}

func hexDigit(character byte) (int, bool) {
	switch {
	case character >= '0' && character <= '9':
		return int(character - '0'), true
	case character >= 'a' && character <= 'f':
		return int(character-'a') + 10, true
	case character >= 'A' && character <= 'F':
		return int(character-'A') + 10, true
	}
	return 0, false
}
