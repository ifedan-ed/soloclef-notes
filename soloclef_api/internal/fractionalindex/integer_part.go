package fractionalindex

import "strings"

// integerLength is `get_integer_length`: the head letter says how many digits
// follow it. A lowercase head counts up from 'a' and an uppercase one counts
// down from 'Z', which is what lets a key grow in both directions.
func integerLength(head rune) (int, error) {
	switch {
	case head >= 'a' && head <= 'z':
		return int(head-'a') + 2, nil
	case head >= 'A' && head <= 'Z':
		return int('Z'-head) + 2, nil
	}
	return 0, ErrInvalidHead
}

// integerPart is `get_integer_part`: the head and its digits, without the
// fraction.
func integerPart(key []rune) ([]rune, error) {
	if len(key) == 0 {
		// Python raises IndexError on key[0] rather than the library's own
		// error. Both reach the route's `except Exception`.
		return nil, ErrInvalidKey
	}
	length, err := integerLength(key[0])
	if err != nil {
		return nil, err
	}
	if length > len(key) {
		return nil, ErrInvalidKey
	}
	return key[:length], nil
}

// validateInteger is `validate_integer`.
func validateInteger(integer []rune) error {
	if len(integer) == 0 {
		return ErrInvalidKey
	}
	length, err := integerLength(integer[0])
	if err != nil {
		return err
	}
	if len(integer) != length {
		return ErrInvalidInteger
	}
	return nil
}

// validateOrderKey is `validate_order_key`. The smallest key of all is refused
// because nothing can sort before it.
func validateOrderKey(key []rune) error {
	if compare(key, smallestInteger()) == 0 {
		return ErrInvalidKey
	}
	integer, err := integerPart(key)
	if err != nil {
		return err
	}
	fraction := key[len(integer):]
	if len(fraction) > 0 && fraction[len(fraction)-1] == zero {
		return ErrInvalidKey
	}
	return nil
}

// digitValue is `digits.index(c)`, which raises for a character the alphabet
// does not hold.
func digitValue(c rune) (int, error) {
	if c > 127 {
		return 0, ErrUnknownDigit
	}
	index := strings.IndexByte(digits, byte(c))
	if index < 0 {
		return 0, ErrUnknownDigit
	}
	return index, nil
}

// incrementInteger is `increment_integer`. The second result is false for the
// library's None — the integer part is already the largest there is.
func incrementInteger(integer []rune) ([]rune, bool, error) {
	if err := validateInteger(integer); err != nil {
		return nil, false, err
	}
	head := integer[0]
	rest := append([]rune(nil), integer[1:]...)
	carry := true
	for index := len(rest) - 1; index >= 0; index-- {
		value, err := digitValue(rest[index])
		if err != nil {
			return nil, false, err
		}
		if value+1 == len(digits) {
			rest[index] = zero
			continue
		}
		rest[index] = rune(digits[value+1])
		carry = false
		break
	}
	if !carry {
		return concat([]rune{head}, rest), true, nil
	}
	switch head {
	case 'Z':
		return []rune{'a', zero}, true, nil
	case 'z':
		return nil, false, nil
	}
	next := head + 1
	if next > 'a' {
		rest = append(rest, zero)
	} else {
		rest = rest[:len(rest)-1]
	}
	return concat([]rune{next}, rest), true, nil
}

// decrementInteger is `decrement_integer`, the mirror image of the above.
func decrementInteger(integer []rune) ([]rune, bool, error) {
	if err := validateInteger(integer); err != nil {
		return nil, false, err
	}
	head := integer[0]
	rest := append([]rune(nil), integer[1:]...)
	last := rune(digits[len(digits)-1])
	borrow := true
	for index := len(rest) - 1; index >= 0; index-- {
		value, err := digitValue(rest[index])
		if err != nil {
			return nil, false, err
		}
		if value-1 == -1 {
			rest[index] = last
			continue
		}
		rest[index] = rune(digits[value-1])
		borrow = false
		break
	}
	if !borrow {
		return concat([]rune{head}, rest), true, nil
	}
	switch head {
	case 'a':
		return []rune{'Z', last}, true, nil
	case 'A':
		return nil, false, nil
	}
	next := head - 1
	if next < 'Z' {
		rest = append(rest, last)
	} else {
		rest = rest[:len(rest)-1]
	}
	return concat([]rune{next}, rest), true, nil
}
