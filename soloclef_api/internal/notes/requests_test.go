package notes

import (
	"strings"
	"testing"
)

const jsonType = "application/json"

func TestACreationReadsEveryField(t *testing.T) {
	creation, failures, err := ReadCreation(jsonType,
		[]byte(`{"title":"T","content":"Body","notebook_id":3,"thread_id":4,"message_id":5}`))
	if err != nil || len(failures) > 0 {
		t.Fatalf("%v %v", err, failures)
	}
	if creation.Title != "T" || creation.Text != "Body" ||
		*creation.NotebookID != 3 || *creation.ThreadID != 4 || *creation.MessageID != 5 {
		t.Errorf("creation %+v", creation)
	}
}

func TestAnEmptyCreationIsAnUntitledEmptyNote(t *testing.T) {
	creation, failures, err := ReadCreation(jsonType, []byte(`{}`))
	if err != nil || len(failures) > 0 || creation.Title != "" || creation.Text != "" || creation.NotebookID != nil {
		t.Errorf("%+v %v %v", creation, failures, err)
	}
}

func TestAMessageNeedsItsThread(t *testing.T) {
	_, failures, _ := ReadCreation(jsonType, []byte(`{"content":"x","message_id":5}`))
	if len(failures) != 1 || failures[0].Location[1] != "message_id" {
		t.Errorf("failures %v", failures)
	}
}

func TestANulOrAnOversizedTextIsRefused(t *testing.T) {
	_, failures, _ := ReadCreation(jsonType, []byte(`{"title":"a\u0000b","content":"c\u0000d"}`))
	if len(failures) != 2 {
		t.Errorf("failures %v", failures)
	}
	huge := `{"content":"` + strings.Repeat("a", MaxTextBytes+1) + `"}`
	_, failures, _ = ReadCreation(jsonType, []byte(huge))
	if len(failures) != 1 || failures[0].Message != textTooLong {
		t.Errorf("failures %v", failures)
	}
}

func TestAChangeLeavesAloneWhatItDoesNotName(t *testing.T) {
	change, failures, _ := ReadChange(jsonType, []byte(`{"content":"New"}`))
	if len(failures) > 0 || change.Title != nil || change.Text == nil || *change.Text != "New" {
		t.Errorf("%+v %v", change, failures)
	}
	change, _, _ = ReadChange(jsonType, []byte(`{"content":null}`))
	if change.Text == nil || *change.Text != "" {
		t.Errorf("a null text empties the note: %+v", change)
	}
	_, failures, _ = ReadChange(jsonType, []byte(`{"title":null}`))
	if len(failures) != 1 || failures[0].Location[1] != "title" {
		t.Errorf("a null title is refused: %v", failures)
	}
}

func TestAFilingMustNameTheNotebookOrNull(t *testing.T) {
	if _, failures, _ := ReadFiling(jsonType, []byte(`{}`)); len(failures) != 1 {
		t.Errorf("an empty body was read as a filing: %v", failures)
	}
	filing, failures, _ := ReadFiling(jsonType, []byte(`{"notebook_id":null}`))
	if len(failures) > 0 || filing.NotebookID != nil {
		t.Errorf("%+v %v", filing, failures)
	}
	filing, _, _ = ReadFiling(jsonType, []byte(`{"notebook_id":9}`))
	if filing.NotebookID == nil || *filing.NotebookID != 9 {
		t.Errorf("%+v", filing)
	}
}
