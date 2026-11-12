package publicchat

import (
	"context"
	"math/big"
	"sort"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
)

// upcastLegacyFormats is _upcast_legacy_public_artifact_formats: read-only
// compatibility for snapshots made before a saved artifact's tool result
// carried its `format`. It returns the snapshot's messages, with the format
// of each such artifact read off the artifact as it is now and written into
// the result.
//
// It is the one place the page of a *chat* reads a second table, and it only
// does so for a snapshot that needs it: no `save_artifact` result without a
// format, no statement.
//
// What the Python function does with values of the wrong type, in its order:
//
//   - `allowed_ids = set(snapshot_data.get("artifact_ids") or [])` runs
//     before anything else and for every chat, messages or not. A list
//     holding a list or an object cannot be made into a set, so such a
//     snapshot is a 500 on every request.
//   - the first loop collects an id only when `isinstance(id, int)` holds and
//     the id is in that set -- `True` is an int and equals 1. What a `True`
//     among the collected ids then does to the statement is askable's.
//   - the second loop asks no such thing. It looks every qualifying result's
//     `artifact_id` up in the dict the query returned, whatever type it is:
//     31.0 finds artifact 31 and is given its format, "31" finds nothing, and
//     a list is unhashable, which raises. So a result the first loop skipped
//     can still be written to -- or fail the request -- once some *other*
//     result has caused the query to run.
func upcastLegacyFormats(ctx context.Context, rows Rows, data *pythonjson.Object) (any, error) {
	var messages any = []any{}
	if stored, present := data.Get("messages"); present {
		messages = stored
	}
	allowed, err := allowedIDs(data.GetOrNil("artifact_ids"))
	if err != nil {
		return nil, err
	}
	entries, err := iterated(messages)
	if err != nil {
		return nil, err
	}

	// Which artifacts to ask about: a Python set, added to in message order.
	missing := &intSet{}
	for _, result := range legacyResults(entries) {
		stored := result.GetOrNil("artifact_id")
		id, isInt := pythonInt(stored)
		if !isInt {
			continue
		}
		for _, candidate := range allowed {
			if equalsInt(candidate, id) {
				_, isBool := stored.(bool)
				missing.Add(id, isBool)
				break
			}
		}
	}
	if missing.used == 0 {
		return messages, nil
	}
	ids, err := askable(missing)
	if err != nil {
		return nil, err
	}
	formats, err := rows.ArtifactFormats(ctx, ids)
	if err != nil {
		return nil, err
	}

	// The Python function deep-copies the messages and writes into the copy.
	// These were decoded for this request and are read by nobody else, so
	// writing into them directly leaves the same answer.
	for _, result := range legacyResults(entries) {
		format, found, err := formatFor(formats, result.GetOrNil("artifact_id"))
		if err != nil {
			return nil, err
		}
		// `if artifact_format:` -- an artifact with an empty format gives
		// nothing. Assigning replaces a falsy `format` where it stands and
		// appends the key otherwise.
		if found && format != "" {
			result.Set("format", format)
		}
	}
	return messages, nil
}

// askable is the ids of `Artifact.id.in_(missing_format_ids)` as a statement
// can carry them, or the failure the Python statement ends in.
//
// SQLAlchemy does not type an IN list from the column it is compared with
// when it can type it from the values: it looks at the **first** value of the
// list, and a bool there makes the whole list BOOLEAN. Then either every
// value is a bool, and Postgres refuses `integer IN (boolean)`, or one is
// not, and SQLAlchemy itself raises "Value 900 is not None, True, or False"
// before anything is sent. Both are the 500. When the first value is an
// integer the list is INTEGER, and a bool further along is sent as 1 or 0 --
// asyncpg encodes it so -- and the statement runs.
//
// "First" is the first value the *set* yields, which is the hash table's
// order and not the order the ids were met in (python_set.go). The list sent
// from here is sorted: with `= ANY(...)` its order is not read, and two
// identical requests should send identical statements.
func askable(missing *intSet) ([]int64, error) {
	ordered := missing.Ordered()
	if ordered[0].isBool {
		return nil, raised("an IN list whose first value is a bool is typed BOOLEAN")
	}
	ids := make([]int64, 0, len(ordered))
	for _, entry := range ordered {
		if !entry.value.IsInt64() {
			// Wider than any integer the driver can carry. asyncpg refuses
			// to encode it and the statement fails; so does this.
			return nil, requestnumber.ErrOutOfRange
		}
		ids = append(ids, entry.value.Int64())
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

// allowedIDs is `set(artifact_ids or [])`, as the values an integer can be
// looked for among.
//
//	anything falsy -> the empty set
//	a list         -> its items, each of which must be hashable
//	a string       -> its characters, and an object -> its keys: both are
//	                  sets of strings, which no integer is in
//	a number, true -> TypeError; not iterable
func allowedIDs(artifactIDs any) ([]any, error) {
	if !truthy(artifactIDs) {
		return nil, nil
	}
	switch held := artifactIDs.(type) {
	case []any:
		for _, item := range held {
			if !hashable(item) {
				return nil, raised("set() of a list holding a list or an object")
			}
		}
		return held, nil
	case string, *pythonjson.Object:
		return nil, nil
	}
	return nil, raised("set() of a value that is not iterable")
}

// legacyResults is the test both of the Python function's loops apply before
// they differ: the `result` of every `save_artifact` part that says it was
// saved and has no format yet.
//
// A message that is not an object, content that is not a list and a part
// that is not an object are each skipped rather than refused. The part's
// `type` is not looked at; only its `toolName`.
func legacyResults(messages []any) []*pythonjson.Object {
	var results []*pythonjson.Object
	for _, entry := range messages {
		message, isObject := entry.(*pythonjson.Object)
		if !isObject {
			continue
		}
		content, isList := message.GetOrNil("content").([]any)
		if !isList {
			continue
		}
		for _, item := range content {
			part, isObject := item.(*pythonjson.Object)
			if !isObject || part.GetOrNil("toolName") != "save_artifact" {
				continue
			}
			result, isObject := part.GetOrNil("result").(*pythonjson.Object)
			if !isObject || result.GetOrNil("status") != "saved" ||
				truthy(result.GetOrNil("format")) {
				continue
			}
			results = append(results, result)
		}
	}
	return results
}

// formatFor is `format_by_id.get(artifact_id)`, where the dict is keyed by
// the integer ids the query returned and the key asked for is a value of any
// JSON type. A list or an object cannot be hashed, which raises before the
// dict is even consulted; a number finds the id it equals; anything else
// finds nothing.
func formatFor(formats map[int64]string, artifactID any) (string, bool, error) {
	if !hashable(artifactID) {
		return "", false, raised("dict.get() with a key that is a list or an object")
	}
	asked, isNumber := numberOf(artifactID)
	if !isNumber {
		return "", false, nil
	}
	for id, format := range formats {
		if asked.equals(number{whole: big.NewInt(id)}) {
			return format, true, nil
		}
	}
	return "", false, nil
}
