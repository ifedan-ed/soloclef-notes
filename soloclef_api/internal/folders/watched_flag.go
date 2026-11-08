package folders

import "git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"

// watchedCleared is the whole of stop_watching_folder's body:
//
//	if folder.folder_metadata and isinstance(folder.folder_metadata, dict):
//	    folder.folder_metadata = {**folder.folder_metadata, "watched": False}
//
// It returns the column's new bytes and whether to write them at all. Four
// things in those two lines decide a row, and each was checked against the
// running tier one row at a time:
//
//  1. **`and` is truthiness, not presence.** A column holding `{}` is falsy, so
//     an empty object is left alone and never gains a `watched` key. So is SQL
//     NULL and so is the JSON scalar `null`.
//  2. **`isinstance(…, dict)`** means a column holding a list is truthy and
//     still left alone — and the route answers 200 either way, so nothing in
//     the response says which happened.
//  3. **The flush is skipped when the dict is unchanged.** Only `watched` can
//     differ, since every other key is carried over, so the write happens only
//     when the stored `watched` is not Python-equal to `False`. And Python's
//     `False == 0` is **true**, so a column holding `{"watched": 0}` is left
//     holding the number zero rather than being rewritten to `false`. A
//     column holding `{"watched": []}` is rewritten, because `False == []` is
//     not.
//  4. **The whole column is rewritten, through json.dumps.** A number the
//     column held more precisely than a float64 comes back rounded:
//     `{"precise": 0.1234567890123456789012345}` is stored as
//     `0.12345678901234568` once the flag is written. That is a defect of the
//     Python route and it is mirrored rather than fixed, which is why this
//     re-renders the decoded value with internal/pythonjson.MarshalColumn
//     instead of reaching for jsonb_set — jsonb_set would have kept the digits
//     and left a different row behind.
func watchedCleared(stored []byte) ([]byte, bool, error) {
	if len(stored) == 0 {
		return nil, false, nil
	}
	value, err := pythonjson.Decode(stored)
	if err != nil {
		return nil, false, err
	}
	object, isObject := value.(*pythonjson.Object)
	if !isObject || object.Len() == 0 {
		return nil, false, nil
	}
	if equalsFalse(object.GetOrNil("watched")) {
		return nil, false, nil
	}

	cleared := pythonjson.NewObject()
	for _, key := range object.Keys() {
		cleared.Set(key, object.GetOrNil(key))
	}
	cleared.Set("watched", false)
	rendered, err := pythonjson.MarshalColumn(cleared)
	if err != nil {
		return nil, false, err
	}
	return rendered, true, nil
}

// equalsFalse is Python's `value == False`, which is true for the boolean and
// for any number equal to zero — `0`, `0.0` and `-0.0` among them — and false
// for None, for a string and for an empty list or object.
func equalsFalse(value any) bool {
	switch typed := value.(type) {
	case bool:
		return !typed
	case pythonjson.RawInt:
		return isZeroInteger(string(typed))
	case float64:
		return typed == 0
	}
	return false
}

// isZeroInteger reads an integer of any width without parsing it, because the
// column may hold one wider than an int64.
func isZeroInteger(digits string) bool {
	if digits == "" {
		return false
	}
	if digits[0] == '-' || digits[0] == '+' {
		digits = digits[1:]
	}
	if digits == "" {
		return false
	}
	for index := 0; index < len(digits); index++ {
		if digits[index] != '0' {
			return false
		}
	}
	return true
}
