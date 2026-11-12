package drivesyncjob

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const alice = "11111111-1111-4111-8111-111111111111"

// What one side writes, the other reads, through the JSON the queue
// carries it as.
func TestARequestSurvivesTheQueue(t *testing.T) {
	for _, sent := range []Request{
		{ConnectorID: 7, UserID: alice, FileIDs: []string{"1AbCdEfGhIjKlMnOp", "file_id-with-dash"}},
		{ConnectorID: 9, UserID: alice},
	} {
		wire, err := json.Marshal(sent.Kwargs())
		if err != nil {
			t.Fatal(err)
		}
		var kwargs map[string]any
		if err := json.Unmarshal(wire, &kwargs); err != nil {
			t.Fatal(err)
		}
		read, err := Read(kwargs)
		if err != nil || !reflect.DeepEqual(read, sent) {
			t.Errorf("sent %+v, read %+v (%v)", sent, read, err)
		}
	}
}

func TestAMessageThatNamesNoAccountOrAStrangeFileIsRefused(t *testing.T) {
	tooMany := make([]any, MaxFiles+1)
	for index := range tooMany {
		tooMany[index] = "1AbCdEfGhIjKlMnOp"
	}
	for name, kwargs := range map[string]map[string]any{
		"no connection":    {"user_id": alice, "file_ids": []any{}},
		"a fraction":       {"connector_id": 1.5, "user_id": alice, "file_ids": []any{}},
		"no account":       {"connector_id": 7.0, "file_ids": []any{}},
		"not an account":   {"connector_id": 7.0, "user_id": "everyone", "file_ids": []any{}},
		"no list":          {"connector_id": 7.0, "user_id": alice},
		"a path as a file": {"connector_id": 7.0, "user_id": alice, "file_ids": []any{"../../about?x="}},
		"a number as file": {"connector_id": 7.0, "user_id": alice, "file_ids": []any{12345678901.0}},
		"too many files":   {"connector_id": 7.0, "user_id": alice, "file_ids": tooMany},
	} {
		if _, err := Read(kwargs); err == nil {
			t.Errorf("%s was read as a request", name)
		}
	}
	if ValidFileID(strings.Repeat("a", 201)) || ValidFileID("short") || !ValidFileID("1AbCdEfGhIjKlMnOp") {
		t.Error("the file id rule is not the one stated")
	}
}
