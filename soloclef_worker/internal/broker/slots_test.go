package broker

import "testing"

func TestSlotsHoldAQueueToItsLimit(t *testing.T) {
	slots := newSlots(map[string]int{"surfsense.heavy": 2}, nil)
	if !slots.takeQueue("surfsense.heavy") || !slots.takeQueue("surfsense.heavy") {
		t.Fatal("the first two should fit")
	}
	if slots.takeQueue("surfsense.heavy") {
		t.Fatal("the third exceeded the limit")
	}
	slots.release("surfsense.heavy", "")
	if !slots.takeQueue("surfsense.heavy") {
		t.Fatal("a released slot should be reusable")
	}
}

func TestOpenQueuesLeavesOutTheFullOnes(t *testing.T) {
	slots := newSlots(map[string]int{"a": 1, "b": 1}, nil)
	slots.takeQueue("a")
	open := slots.openQueues([]string{"a", "b"})
	if len(open) != 1 || open[0] != "b" {
		t.Fatalf("openQueues = %q", open)
	}
}

// A task class limit is the point of the tier: four heavy task names share one
// queue, and one of them hogging it is what the Celery worker could not stop.
func TestTaskClassLimitIsSeparateFromTheQueueLimit(t *testing.T) {
	slots := newSlots(map[string]int{"surfsense.heavy": 8}, map[string]int{"podcast.render_audio": 1})
	if !slots.takeTask("podcast.render_audio") {
		t.Fatal("the first render should fit")
	}
	if slots.takeTask("podcast.render_audio") {
		t.Fatal("the second render exceeded the task class limit")
	}
	if !slots.takeTask("generate_video_presentation") {
		t.Fatal("another class must not be blocked by a full one")
	}
}

// A task with no limit of its own is bounded by its queue, which is a stated
// rule rather than an unlimited default.
func TestATaskWithNoLimitOfItsOwnIsAlwaysAdmitted(t *testing.T) {
	slots := newSlots(map[string]int{"surfsense": 1}, map[string]int{"other": 1})
	for i := 0; i < 50; i++ {
		if !slots.takeTask("index_document") {
			t.Fatalf("admission %d refused", i)
		}
	}
}

func TestInFlightCountsAreReportedPerQueueAndPerTask(t *testing.T) {
	slots := newSlots(map[string]int{"surfsense.heavy": 4}, nil)
	slots.takeQueue("surfsense.heavy")
	slots.takeTask("podcast.render_audio")
	byQueue, byTask := slots.inFlight()
	if byQueue["surfsense.heavy"] != 1 || byTask["podcast.render_audio"] != 1 {
		t.Fatalf("inFlight = %v / %v", byQueue, byTask)
	}
	slots.release("surfsense.heavy", "podcast.render_audio")
	byQueue, byTask = slots.inFlight()
	if len(byQueue) != 0 || len(byTask) != 0 {
		t.Fatalf("after release: %v / %v", byQueue, byTask)
	}
}

func TestFreedSlotsWakeAWaitingConsumer(t *testing.T) {
	slots := newSlots(map[string]int{"a": 1}, nil)
	slots.takeQueue("a")
	waiting := slots.freed()
	slots.release("a", "")
	select {
	case <-waiting:
	default:
		t.Fatal("releasing a slot must wake a consumer waiting for one")
	}
}
