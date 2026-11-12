package markdownmemory

import (
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// ReadRequest validates the body of the PUT route: one required string and
// nothing else (MemoryUpdate), with no length constraint, because Save
// checks the length itself and in its own words.
func ReadRequest(contentType string, body []byte) (string, []errorenvelope.Failure, error) {
	object, failure, err := requestbody.Decode(contentType, body)
	if err != nil {
		return "", nil, err
	}
	if failure != nil {
		return "", []errorenvelope.Failure{*failure}, nil
	}
	model := requestbody.NewModel(object)
	memory := model.Text("memory_md", 0, 0)
	if !model.Valid() {
		return "", model.Failures(), nil
	}
	return memory, nil, nil
}
