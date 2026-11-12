package requestbody

import "strconv"

// NumberList reads a required `list[int]` field with pydantic's own length
// constraints, which is what `message_ids: list[int] = Field(..., min_length=1,
// max_length=200)` is (app/schemas/chat_comments.py:CommentBatchRequest).
//
// The order of the three checks is pydantic-core's and was asked of the running
// tier one case at a time, because it is not the order a reader would guess:
//
//	[]                     -> "List should have at least 1 item after validation, not 0"
//	201 numbers            -> "List should have at most 200 items after validation, not 201"
//	201 numbers, one bad   -> the same message, and **not** the item's
//	["x", "y"]             -> both items refused, under "0" and "1"
//	null                   -> "Input should be a valid list"
//
// So the maximum is checked against the input's length before any item is
// read, while the minimum is checked after — which "after validation" says and
// which only the 201-with-a-bad-item case makes visible.
//
// An item is coerced exactly as a scalar `int` field is, including pydantic's
// lax branches: `"1 "` and `"1_0"` are read, `true` is 1, and an integer no
// column can hold is held by Python and fails in the query, so it is reported
// as out of range rather than as a validation failure (integer.go).
func (m *Model) NumberList(name string, minItems, maxItems int) []int64 {
	value, present := m.body.Get(name)
	if !present {
		m.reject(FieldRequired, name)
		return nil
	}
	items, isList := value.([]any)
	if !isList {
		m.reject(NotAList, name)
		return nil
	}
	if maxItems > 0 && len(items) > maxItems {
		m.reject(atMostItems(maxItems, len(items)), name)
		return nil
	}

	numbers := make([]int64, 0, len(items))
	for index, item := range items {
		before := len(m.Failures())
		number := m.numberAt(item, []string{name, itemIndex(index)})
		if len(m.Failures()) == before {
			numbers = append(numbers, number)
		}
	}
	if !m.Valid() {
		return nil
	}

	if minItems > 0 && len(items) < minItems {
		m.reject(atLeastItems(minItems, len(items)), name)
		return nil
	}
	return numbers
}

// atLeastItems and atMostItems word a list's length the way pydantic words it,
// which names the limit and the length that was sent and pluralises the first
// of the two only.
func atLeastItems(limit, got int) string {
	return "List should have at least " + items(limit) +
		" after validation, not " + strconv.Itoa(got)
}

func atMostItems(limit, got int) string {
	return "List should have at most " + items(limit) +
		" after validation, not " + strconv.Itoa(got)
}

func items(count int) string {
	if count == 1 {
		return "1 item"
	}
	return strconv.Itoa(count) + " items"
}
