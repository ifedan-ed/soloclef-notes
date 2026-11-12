package openaicompat

import (
	"encoding/json"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
)

// A message's Mark is the caller's label for finding the message again. It
// is stored with the conversation and must never reach a model: a provider
// would refuse the unknown field, or worse, read it.
func TestAMessagesMarkIsStoredAndNeverSent(t *testing.T) {
	marked := model.Message{Role: model.RoleUser, Content: "What is mitosis?", Mark: `{"turn":"7:1731315600000"}`}
	body, err := encodeRequest("m", nil, model.Request{Messages: []model.Message{marked}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "mark") || strings.Contains(string(body), "1731315600000") {
		t.Fatalf("the mark was sent: %s", body)
	}
	// It is part of the stored form, and a message without one stores no
	// empty field.
	stored, _ := json.Marshal(marked)
	var back model.Message
	if err := json.Unmarshal(stored, &back); err != nil || back.Mark != marked.Mark {
		t.Fatalf("stored %s, read back %+v, %v", stored, back, err)
	}
	if plain, _ := json.Marshal(model.User("x")); strings.Contains(string(plain), "mark") {
		t.Errorf("an unmarked message stores %s", plain)
	}
}
