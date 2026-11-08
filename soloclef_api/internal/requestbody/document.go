package requestbody

import (
	"mime"
	"strings"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// Decode reads the body as the object a pydantic model is validated against.
//
// It returns either the object, or the one model-level failure FastAPI reports
// instead, or ErrUndecodable for the bodies whose fault only the Python tier's
// parser can place (see the package comment).
func Decode(contentType string, body []byte) (*pythonjson.Object, *errorenvelope.Failure, error) {
	if !readsAsJSON(contentType) {
		// FastAPI reads the body as JSON only for these media types; for any
		// other it hands pydantic something that is not a mapping, which is the
		// model-level refusal rather than a complaint about the header.
		return nil, modelFailure(NotAnObject), nil
	}
	if len(body) == 0 {
		return nil, modelFailure(FieldRequired), nil
	}
	if !utf8.Valid(body) {
		return nil, nil, ErrUndecodable
	}
	if carriesUnpairedSurrogate(body) {
		return nil, nil, ErrUndecodable
	}

	value, err := pythonjson.Decode(body)
	if err != nil {
		return nil, nil, ErrUndecodable
	}
	if value == nil {
		// An explicit JSON null is the model being absent, not a bad mapping.
		return nil, modelFailure(FieldRequired), nil
	}
	object, isObject := value.(*pythonjson.Object)
	if !isObject {
		return nil, modelFailure(NotAnObject), nil
	}
	return object, nil, nil
}

// readsAsJSON is the media-type test FastAPI applies before parsing a body as
// JSON: the type itself, or any type whose subtype ends in +json. Parameters
// such as charset are ignored, and a request with no content type at all is not
// one of them.
func readsAsJSON(contentType string) bool {
	media, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return media == "application/json" || strings.HasSuffix(media, "+json")
}

func modelFailure(message string) *errorenvelope.Failure {
	return &errorenvelope.Failure{Location: []string{BodyRoot}, Message: message}
}
