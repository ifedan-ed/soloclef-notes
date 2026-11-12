package routing

import (
	"reflect"
	"testing"
)

var live = Table{Default: "surfsense"}

// The queues are built from the default queue's name, so the names here are
// the names the deployment's worker consumes.
func TestTheQueueNamesAreTheDeployedOnes(t *testing.T) {
	want := []string{"surfsense", "surfsense.heavy"}
	if got := live.Queues(); !reflect.DeepEqual(got, want) {
		t.Errorf("Queues = %q", got)
	}
}

// Routing a heavy task to the default queue is a minutes-long job in front of
// a file upload; routing a fast task to the heavy queue puts it behind one.
func TestEveryRoutedTaskGoesWhereItBelongs(t *testing.T) {
	cases := map[string]string{
		"podcast.draft_transcript":           "surfsense.heavy",
		"deliverables.execute_queued":        "surfsense.heavy",
		"notebook_autoname":                  "surfsense",
		"delete_folder_documents_background": "surfsense",
		"erase_account":                      "surfsense",
		"":                                   "surfsense",
	}
	for task, want := range cases {
		if got := live.QueueFor(task); got != want {
			t.Errorf("QueueFor(%q) = %q, want %q", task, got, want)
		}
	}
}
