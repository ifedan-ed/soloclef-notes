package principal

import (
	"encoding/base64"
	"errors"
	"strings"
)

// alphabet is base64url's, which is what PyJWT translates its input into before
// handing it to binascii.
const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"

// errUnreadableSegment is every way binascii refuses a segment.
var errUnreadableSegment = errors.New("the segment is not readable base64")

// decodePythonBase64URL decodes a JWT segment as PyJWT's base64url_decode
// does. That is not what a strict decoder does, and it is not a detail: it
// decides whether the two tiers agree that a request carries an agent's token
// at all, and so which 401 a malformed token gets.
//
// PyJWT pads the input to a multiple of four and hands it to binascii in its
// lenient mode, which has three behaviours a strict decoder does not:
//
//   - characters outside the alphabet are discarded, so junk inside a segment
//     is read straight through;
//   - a padding run ends the input, so anything after it is ignored;
//   - the padding has to finish the group it ends, and the padding count comes
//     from the *unstripped* length — which is why a segment with one trailing
//     space is refused and one with three is read.
//
// The cases in python_base64_test.go came out of PyJWT itself, not out of this
// description.
func decodePythonBase64URL(segment string) ([]byte, error) {
	padded := []rune(segment + strings.Repeat("=", (4-len(segment)%4)%4))

	var data []rune
	pending := 0
	for index := 0; index < len(padded); index++ {
		character := padded[index]
		if character == '=' {
			if pending == 0 {
				break
			}
			if pending == 1 {
				return nil, errUnreadableSegment
			}
			needed := 4 - pending
			if index+needed > len(padded) {
				return nil, errUnreadableSegment
			}
			for cursor := index; cursor < index+needed; cursor++ {
				if padded[cursor] != '=' {
					return nil, errUnreadableSegment
				}
			}
			pending = 0
			break
		}
		if strings.ContainsRune(alphabet, character) {
			data = append(data, character)
			if pending++; pending == 4 {
				pending = 0
			}
		}
	}
	if pending != 0 {
		return nil, errUnreadableSegment
	}

	decoded, err := base64.RawURLEncoding.DecodeString(string(data))
	if err != nil {
		return nil, errUnreadableSegment
	}
	return decoded, nil
}
