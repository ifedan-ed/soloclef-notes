package eventfilter

import (
	"encoding/json"
	"testing"
)

func object(t *testing.T, text string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestWhichPayloadsAFilterMatches(t *testing.T) {
	payload := `{"document_id":42,"folder_id":7,"previous_folder_id":null,"document_type":"FILE","title":"Tides"}`
	for _, c := range []struct {
		name, filter string
		want         bool
	}{
		{"an empty filter matches everything", `{}`, true},
		{"a plain value is equality", `{"folder_id":7}`, true},
		{"and a different value is not", `{"folder_id":8}`, false},
		{"sibling keys are all required", `{"folder_id":7,"document_type":"NOTE"}`, false},
		{"a field the payload lacks fails", `{"connector_id":3}`, false},
		{"a present null equals null", `{"previous_folder_id":null}`, true},
		{"operators on one field all hold", `{"document_id":{"$gt":40,"$lte":42}}`, true},
		{"$in", `{"document_type":{"$in":["FILE","NOTE"]}}`, true},
		{"$nin", `{"document_type":{"$nin":["FILE"]}}`, false},
		{"$ne on a missing field is still a miss", `{"connector_id":{"$ne":3}}`, false},
		{"$or", `{"$or":[{"folder_id":1},{"title":"Tides"}]}`, true},
		{"$and", `{"$and":[{"folder_id":7},{"title":"Moon"}]}`, false},
		{"$not", `{"$not":{"document_type":"NOTE"}}`, true},
		{"a number is not ordered against text", `{"title":{"$gt":3}}`, false},
	} {
		got, err := Matches(object(t, c.filter), object(t, payload))
		if err != nil || got != c.want {
			t.Errorf("%s: %v, %v; want %v", c.name, got, err, c.want)
		}
	}
}

// A filter nobody could evaluate is an error whatever the payload, so it is
// refused when it is saved and not found out by a trigger that never fires.
func TestAMisspeltFilterIsAnErrorAndNotAMiss(t *testing.T) {
	for _, filter := range []string{
		`{"folder_id":{"$greater":3}}`, `{"$nor":[]}`, `{"$or":{"folder_id":1}}`, `{"$not":[1]}`,
		`{"folder_id":8,"title":{"$regex":"x"}}`, `{"document_type":{"$in":"FILE"}}`,
	} {
		if err := Validate(object(t, filter)); err == nil {
			t.Errorf("%s was accepted", filter)
		}
		if _, err := Matches(object(t, filter), object(t, `{"folder_id":7}`)); err == nil {
			t.Errorf("%s matched without an error", filter)
		}
	}
}
