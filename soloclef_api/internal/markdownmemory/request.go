package markdownmemory

import (
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// ReadRequest validates the body of either PUT route. Both declare one
// required string and nothing else — MemoryUpdate on the user route and
// TeamMemoryUpdate on the team route — and neither constrains its length,
// because save_memory does that itself and in its own words.
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
