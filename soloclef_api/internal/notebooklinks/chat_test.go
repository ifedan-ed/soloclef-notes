package notebooklinks

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMarkersAreRewrittenOrRemovedAndNothingElseIsTouched(t *testing.T) {
	keepSeven := func(where string) (string, bool) {
		if where == "7" {
			return "[citation:1]", true
		}
		return "", false
	}
	for _, c := range []struct{ name, text, want string }{
		{"no marker", "  indented\n    code  ", "  indented\n    code  "},
		{"a kept marker", "Tides rise [citation:7].", "Tides rise [citation:1]."},
		{"a removed marker takes its space", "Tides rise [citation:9].", "Tides rise."},
		{"the other bracket style and a zero-width space", "Up 【​citation:7​】 down 【citation:8】.", "Up [citation:1] down."},
		{"a web address is removed", "See [citation:https://example.test/a?b=1] now", "See now"},
		{"two in a row", "x [citation:9][citation:7] y", "x[citation:1] y"},
		{"the word in prose", "a citation: is a reference", "a citation: is a reference"},
		{"never closed", "open [citation:7\nnext line]", "open [citation:7\nnext line]"},
	} {
		if got := rewriteMarkers(c.text, keepSeven); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func message(role, content string) chatMessage {
	return decodeMessage(SharedChatRow{Role: role, Content: []byte(content), CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)})
}

func TestAVisitorSeesWordsAndFinishedThingsAndNothingElse(t *testing.T) {
	messages := []chatMessage{
		message("USER", `"What moves the tides?"`),
		message("ASSISTANT", `[
		 {"type":"reasoning","text":"the user wants storage_key=/srv/x"},
		 {"type":"tool-call","toolName":"search_documents","toolCallId":"c1","args":{"query":"secret plan"},"result":{"hits":[{"chunk_id":41}]}},
		 {"type":"text","text":"The moon [citation:41], mostly [citation:99]. Also [citation:41] and the sun [citation:42] [citation:https://example.test]."},
		 {"type":"tool-call","toolName":"save_artifact","toolCallId":"c2","args":{"content":"private draft"},"result":{"status":"saved","artifact_id":70,"files":[{"storage_key":"k"}]}},
		 {"type":"tool-call","toolName":"save_artifact","toolCallId":"c3","args":{},"result":{"status":"refused","artifact_id":71}},
		 {"type":"tool-call","toolName":"generate_podcast","toolCallId":"c4","args":{},"result":{"status":"awaiting_brief","podcast_id":90}},
		 {"type":"tool-call","toolName":"generate_podcast","toolCallId":"c5","args":{},"result":{"podcast_id":91}},
		 {"type":"tool-call","toolName":"enqueue_deliverable_job","toolCallId":"c6","args":{},"result":{"status":"pending","job_id":80}},
		 {"type":"tool-call","toolName":"search_documents","toolCallId":"c7","args":{},"result":{"job_id":80}}
		]`),
		message("SYSTEM", `[{"type":"text","text":"You are a helpful assistant with the owner's instructions."}]`),
		message("ASSISTANT", `[{"type":"reasoning","text":"only thinking"}]`),
		message("ASSISTANT", `{"not":"a list"}`),
	}

	artifacts, podcasts, jobs := madeRefs(messages)
	if len(artifacts) != 1 || artifacts[0] != 70 || len(podcasts) != 2 || podcasts[0] != 90 || len(jobs) != 1 || jobs[0] != 80 {
		t.Fatalf("made: artifacts %v, podcasts %v, jobs %v", artifacts, podcasts, jobs)
	}
	if cited := citedIDs(messages); len(cited) != 3 || cited[0] != 41 || cited[1] != 99 || cited[2] != 42 {
		t.Fatalf("cited passages: %v", cited)
	}

	page := int64(3)
	shown, citations := publicChat(messages,
		// 99 is a passage of a document outside the notebook, so it is not here.
		map[int64]passagePlace{41: {500, &page}, 42: {501, nil}},
		map[int64]string{500: "Moon", 501: "Sun"},
		// The podcast 91 is not finished, so it is not here.
		map[madeRef]madeArtifact{
			{"artifact", 70}: {70, "Tide table", "markdown"},
			{"podcast", 90}:  {72, "Tide talk", "podcast"},
			{"job", 80}:      {73, "Tide film", "video"},
		})
	got, err := json.Marshal(struct {
		Messages  []liveMessage           `json:"messages"`
		Citations map[string]liveCitation `json:"citations"`
	}{shown, citations})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"messages":[` +
		`{"role":"user","content":[{"text":"What moves the tides?","type":"text"}],"created_at":"2026-01-02T03:04:05Z"},` +
		`{"role":"assistant","content":[` +
		`{"text":"The moon [citation:1], mostly. Also [citation:1] and the sun [citation:2].","type":"text"},` +
		`{"args":{},"result":{"artifact_id":70,"format":"markdown","title":"Tide table"},"toolCallId":"artifact-1-1","toolName":"notebook_artifact","type":"tool-call"},` +
		`{"args":{},"result":{"artifact_id":72,"format":"podcast","title":"Tide talk"},"toolCallId":"artifact-1-2","toolName":"notebook_artifact","type":"tool-call"},` +
		`{"args":{},"result":{"artifact_id":73,"format":"video","title":"Tide film"},"toolCallId":"artifact-1-3","toolName":"notebook_artifact","type":"tool-call"}` +
		`],"created_at":"2026-01-02T03:04:05Z"}],` +
		`"citations":{"1":{"source_id":500,"title":"Moon","page":3},"2":{"source_id":501,"title":"Sun","page":null}}}`
	if string(got) != want {
		t.Errorf("the public chat:\n got %s\nwant %s", got, want)
	}
	for _, secret := range []string{"secret plan", "private draft", "storage_key", "thinking", "helpful assistant", "41", "99", "example.test"} {
		if strings.Contains(string(got), secret) {
			t.Errorf("the public chat discloses %q", secret)
		}
	}
}
