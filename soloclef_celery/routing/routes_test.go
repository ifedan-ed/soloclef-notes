package routing

import (
	"reflect"
	"testing"
)

var live = Table{Default: "surfsense"}

// The three queues are built from the default queue's name, so the names here
// are the names the deployment's workers consume (CELERY_QUEUES in
// docker/docker-compose.yml).
func TestTheQueueNamesAreTheDeployedOnes(t *testing.T) {
	want := []string{"surfsense", "surfsense.connectors", "surfsense.heavy"}
	if got := live.Queues(); !reflect.DeepEqual(got, want) {
		t.Errorf("Queues = %q", got)
	}
}

// Routing a heavy task to the default queue is a minutes-long job in front of
// a file upload; routing a fast task to the heavy queue puts it behind one.
func TestEveryRoutedTaskGoesWhereTaskRoutesSendsIt(t *testing.T) {
	cases := map[string]string{
		"podcast.draft_transcript":       "surfsense.heavy",
		"podcast.render_audio":           "surfsense.heavy",
		"generate_video_presentation":    "surfsense.heavy",
		"deliverables.execute_queued":    "surfsense.heavy",
		"index_google_drive_files":       "surfsense.connectors",
		"reindex_knowledge_store":        "surfsense.connectors",
		"delete_document":                "surfsense",
		"index_knowledge_store_revision": "surfsense",
		"":                               "surfsense",
	}
	for task, want := range cases {
		if got := live.QueueFor(task); got != want {
			t.Errorf("QueueFor(%q) = %q, want %q", task, got, want)
		}
	}
}
