// Package fractionalindex is the `fractional_indexing` library's
// `generate_key_between`, which is where a folder's `position` comes from.
//
// It is a mirror of a third-party library rather than of Soloclef's own code,
// and it is here for the same reason internal/pythonjson is: the keys it
// produces are in the response and in the column, so a key that differs by one
// character is a different answer and a different row. Every value below was
// taken from the library's own source (fractional_indexing 0.1.3, CC0) and
// then compared against that library running in this deployment's own backend
// image, function by function (testdata/library_answers.json).
//
// What a reader will want to know first: the algorithm is total only for the
// keys it has itself produced. Asked for a key before a malformed one, or
// between two in the wrong order, the library *raises* — and
// app/routes/folders_routes.py wraps that in `except Exception`, so a reorder
// carrying a position a client made up is a sanitised 500 on both tiers. Those
// cases are errors here for exactly that reason; the messages differ from
// Python's and nothing can see them, because neither tier puts them in a
// response.
//
// # Why it works in runes and not in bytes
//
// Python indexes and slices a `str` by code point, and the library's whole
// arithmetic is indexing and slicing. Written on bytes, this package answered
// a key where the library raises — `generate_key_between(None, "aé")` came
// back `"a\xc3"` here and raises `ValueError` there, and `("a0", "bé")` came
// back `"a1"` where the library refuses the key outright, because `"bé"` is
// two characters to Python and three bytes to Go. Both were found by the
// captured answers below rather than reasoned about, and they are why every
// function here takes a `[]rune`.
package fractionalindex

import "errors"

// digits is BASE_62_DIGITS. The library takes the alphabet as an argument and
// every call site in this app leaves it at the default, so it is a constant
// here: a second alphabet would be a second rule nothing asks for.
const digits = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

const zero = '0'

// The library's own FIError, one per place it raises, plus the two Python
// exceptions its own code lets through. They are separate values so a test can
// say which refusal it provoked; no response ever carries one.
var (
	ErrOutOfOrder      = errors.New("fractionalindex: the keys are not in order")
	ErrTrailingZero    = errors.New("fractionalindex: a key ends in the zero digit")
	ErrInvalidHead     = errors.New("fractionalindex: the key's head is not a letter")
	ErrInvalidInteger  = errors.New("fractionalindex: the key's integer part is the wrong length")
	ErrInvalidKey      = errors.New("fractionalindex: not an order key")
	ErrUnknownDigit    = errors.New("fractionalindex: the key carries a character the alphabet does not")
	ErrCannotDecrement = errors.New("fractionalindex: cannot decrement any more")
	ErrCannotIncrement = errors.New("fractionalindex: cannot increment any more")
)

// KeyBetween is `generate_key_between(a, b)`: a key that sorts after `before`
// and before `after`, with nil standing for the library's None at either end.
func KeyBetween(before, after *string) (string, error) {
	var low, high []rune
	if before != nil {
		low = []rune(*before)
		if err := validateOrderKey(low); err != nil {
			return "", err
		}
	}
	if after != nil {
		high = []rune(*after)
		if err := validateOrderKey(high); err != nil {
			return "", err
		}
	}
	if before != nil && after != nil && compare(low, high) >= 0 {
		return "", ErrOutOfOrder
	}

	switch {
	case before == nil && after == nil:
		return "a" + string(zero), nil
	case before == nil:
		key, err := keyBefore(high)
		return string(key), err
	case after == nil:
		key, err := keyAfter(low)
		return string(key), err
	}
	key, err := keyBetween(low, high)
	return string(key), err
}

// keyBefore is the `a is None` branch: a key that sorts before `after`.
func keyBefore(after []rune) ([]rune, error) {
	integer, err := integerPart(after)
	if err != nil {
		return nil, err
	}
	fraction := after[len(integer):]
	if compare(integer, smallestInteger()) == 0 {
		// The smallest integer part cannot be decremented, so the key is made
		// inside its fractional part instead.
		middle, err := midpoint(nil, fraction, false)
		if err != nil {
			return nil, err
		}
		return concat(integer, middle), nil
	}
	if compare(integer, after) < 0 {
		// `after` carries a fraction, so its own integer part sorts before it.
		return integer, nil
	}
	decremented, ok, err := decrementInteger(integer)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrCannotDecrement
	}
	return decremented, nil
}

// keyAfter is the `b is None` branch: a key that sorts after `before`.
//
// The fall-through reads oddly in the library and is mirrored as written: when
// the integer part cannot be incremented, the key grows in its fractional part
// instead.
func keyAfter(before []rune) ([]rune, error) {
	integer, err := integerPart(before)
	if err != nil {
		return nil, err
	}
	fraction := before[len(integer):]
	incremented, ok, err := incrementInteger(integer)
	if err != nil {
		return nil, err
	}
	if ok {
		return incremented, nil
	}
	middle, err := midpoint(fraction, nil, true)
	if err != nil {
		return nil, err
	}
	return concat(integer, middle), nil
}

// keyBetween is the branch where both ends are keys.
func keyBetween(before, after []rune) ([]rune, error) {
	beforeInteger, err := integerPart(before)
	if err != nil {
		return nil, err
	}
	afterInteger, err := integerPart(after)
	if err != nil {
		return nil, err
	}
	beforeFraction := before[len(beforeInteger):]
	afterFraction := after[len(afterInteger):]
	if compare(beforeInteger, afterInteger) == 0 {
		middle, err := midpoint(beforeFraction, afterFraction, false)
		if err != nil {
			return nil, err
		}
		return concat(beforeInteger, middle), nil
	}
	incremented, ok, err := incrementInteger(beforeInteger)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrCannotIncrement
	}
	if compare(incremented, after) < 0 {
		return incremented, nil
	}
	middle, err := midpoint(beforeFraction, nil, true)
	if err != nil {
		return nil, err
	}
	return concat(beforeInteger, middle), nil
}

// smallestInteger is the one integer part the library refuses as a whole key
// and treats specially as an upper neighbour: 'A' followed by 26 zeros.
func smallestInteger() []rune {
	key := make([]rune, 0, 27)
	key = append(key, 'A')
	for range 26 {
		key = append(key, zero)
	}
	return key
}

// compare orders two keys by code point, which is what Python's `<` does for
// a `str`.
func compare(left, right []rune) int {
	for index := 0; index < len(left) && index < len(right); index++ {
		switch {
		case left[index] < right[index]:
			return -1
		case left[index] > right[index]:
			return 1
		}
	}
	switch {
	case len(left) < len(right):
		return -1
	case len(left) > len(right):
		return 1
	}
	return 0
}

// concat joins two keys without writing through either.
func concat(head, tail []rune) []rune {
	joined := make([]rune, 0, len(head)+len(tail))
	joined = append(joined, head...)
	return append(joined, tail...)
}
