package fractionalindex

// midpoint is the library's `midpoint(a, b, digits)`: the shortest fraction
// that sorts strictly between two of them. `highIsNone` stands for the
// library's `b is None`, which is a different thing from an empty `b` in three
// of the branches below — hence the flag rather than a nil slice, since a
// Python slice of nothing is `""` and not None.
//
// Three details are mirrored rather than tidied, and each of them decides a
// character of a key that ends up in a column:
//
//   - the trailing-zero refusal reads `(a and a[-1]) == zero` in Python, whose
//     precedence makes an empty `a` compare the empty string against the zero
//     digit and so pass. An empty `a` is the ordinary case — it is what
//     `midpoint` is called with for a prepend — so reading that as "the empty
//     key ends in zero" would refuse every prepend.
//   - `round_half_up` is not Go's rounding and not banker's rounding. Both
//     digit values are whole numbers, so half their sum is exact and rounding
//     half up is the integer `(a + b + 1) / 2`.
//   - the `digit_b` of an empty `b` is -1, from an `except IndexError` in the
//     library that nothing in this app can reach: `b` is never empty, because
//     a key never ends in the zero digit and so never equals `a` padded with
//     them. It is kept because removing it would be a judgement about
//     unreachability that the next alphabet could falsify.
func midpoint(low, high []rune, highIsNone bool) ([]rune, error) {
	if !highIsNone && compare(low, high) >= 0 {
		return nil, ErrOutOfOrder
	}
	if len(low) > 0 && low[len(low)-1] == zero {
		return nil, ErrTrailingZero
	}
	if !highIsNone && len(high) > 0 && high[len(high)-1] == zero {
		return nil, ErrTrailingZero
	}

	if !highIsNone && len(high) > 0 {
		// Drop the longest common prefix, padding `low` with zeros as we go.
		// `high` needs no padding: it cannot run out first while the prefix
		// matches.
		shared := 0
		for index := 0; index < len(high); index++ {
			padded := zero
			if index < len(low) {
				padded = low[index]
			}
			if padded != high[index] {
				break
			}
			shared++
		}
		if shared > 0 {
			middle, err := midpoint(sliceFrom(low, shared), high[shared:], false)
			if err != nil {
				return nil, err
			}
			return concat(high[:shared], middle), nil
		}
	}

	// The first digits — or the absence of one — differ.
	lowDigit := 0
	if len(low) > 0 {
		value, err := digitValue(low[0])
		if err != nil {
			return nil, err
		}
		lowDigit = value
	}
	highDigit := len(digits)
	if !highIsNone {
		if len(high) == 0 {
			highDigit = -1
		} else {
			value, err := digitValue(high[0])
			if err != nil {
				return nil, err
			}
			highDigit = value
		}
	}

	if highDigit-lowDigit > 1 {
		return []rune{rune(digits[(lowDigit+highDigit+1)/2])}, nil
	}
	if !highIsNone && len(high) > 1 {
		return high[:1], nil
	}
	// `high` is None or a single digit, so the answer keeps `low`'s first
	// digit and goes on: midpoint("49", "5") is "4" + midpoint("9", None),
	// which becomes "4" + "9" + midpoint("", None) — "495".
	rest, err := midpoint(sliceFrom(low, 1), nil, true)
	if err != nil {
		return nil, err
	}
	return concat([]rune{rune(digits[lowDigit])}, rest), nil
}

// sliceFrom is Python's `a[n:]`, which is the empty string rather than a panic
// when `n` is past the end.
func sliceFrom(key []rune, from int) []rune {
	if from >= len(key) {
		return nil
	}
	return key[from:]
}
