package activitylogs

import (
	"errors"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// errNotAMapping is a log_metadata column holding something other than an
// object. LogRead types it `dict[str, Any] | None`, so the Python route's
// response model rejects it and FastAPI answers 500; this tier must not invent
// a response the Python tier would not have written.
var errNotAMapping = errors.New("activitylogs: log_metadata is not an object")

// Build renders one log as the routes' LogRead.
//
// The key order is pydantic's field order, which for
// `class LogRead(LogBase, IDModel, TimestampModel)` is the reverse of the
// method resolution order: created_at and id come from the two mixins and so
// come first, then the entry's five own fields.
//
// The datetime is pydantic's own form rather than isoformat's, because the
// routes declare a response_model.
func Build(entry Entry) (*pythonjson.Object, error) {
	metadata, err := readMetadata(entry.Metadata)
	if err != nil {
		return nil, err
	}

	rendered := pythonjson.NewObject()
	rendered.Set("created_at", pythonjson.PydanticDateTime(entry.CreatedAt))
	rendered.Set("id", entry.ID)
	rendered.Set("level", entry.Level)
	rendered.Set("status", entry.Status)
	rendered.Set("message", entry.Message)
	rendered.Set("source", textOrNil(entry.Source))
	rendered.Set("log_metadata", metadata)
	return rendered, nil
}

// readMetadata reads the json column as pydantic reads it into
// `dict[str, Any] | None`. A SQL NULL is None; anything that is not an object
// fails the response model, which is the Python route's 500 and so is an error
// here rather than a guess.
func readMetadata(stored []byte) (any, error) {
	if len(stored) == 0 {
		return nil, nil
	}
	value, err := pythonjson.Decode(stored)
	if err != nil {
		return nil, err
	}
	if value == nil {
		return nil, nil
	}
	object, isObject := value.(*pythonjson.Object)
	if !isObject {
		return nil, errNotAMapping
	}
	// These routes declare a response_model, so the whole body — the metadata
	// included — is serialised by pydantic-core rather than by json.dumps, and
	// a float inside the column is written ryu's way and not repr's. The log
	// summary beside them has no response_model and so renders the same column
	// the other way (internal/pythonjson.PydanticFloat).
	return pythonjson.PydanticValues(object), nil
}

func textOrNil(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

// marshal is the one place this slice renders a body to bytes, so the routes
// and the test cannot drift into two renderings.
func marshal(rendered *pythonjson.Object) (string, error) {
	body, err := pythonjson.Marshal(rendered)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
