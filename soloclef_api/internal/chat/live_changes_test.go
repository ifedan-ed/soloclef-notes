package chat

import (
	"net/http"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"

	"git.danvics.com/danvics/soloclef/soloclef_notify/livechange"
)

// A turn tells its owner's other screens (another tab, the phone) that the
// thread moved: when it starts, so they show it running, and when its answer
// is saved and it is named, so they show the answer and the title. Each
// names the thread, and nobody else is told.
func TestATurnTellsTheOwnersOtherScreensAboutItsThread(t *testing.T) {
	script := modeltest.NewScript(modeltest.Reply{Text: "Four phases.", Usage: model.Usage{TotalTokens: 5}})
	a := newApp(t, script, []tool.Tool{})
	response := a.request(ada, http.MethodPost, TurnPath, turnBody)
	defer response.Body.Close()
	if _, done := readFrames(t, response.Body); !done {
		t.Fatal("the stream did not end")
	}
	calls := a.changes.Calls()
	if len(calls) < 3 {
		t.Fatalf("told %d times, want the start, the saved answer and the title: %+v", len(calls), calls)
	}
	for _, call := range calls {
		if call.UserID != ada {
			t.Errorf("told %s", call.UserID)
		}
		for _, change := range call.Changes {
			if change.Kind != livechange.Chats || change.ID != 1 {
				t.Errorf("change %+v, want chats of thread 1", change)
			}
		}
	}
}
