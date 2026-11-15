package studiogenerate

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

// A notebook titled past about 185 characters makes a default title longer
// than the bound. Every generate in it used to be a 422; now each starts,
// listed under the cut title and headed by the whole notebook title.
func TestANotebookWithALongTitleStillGenerates(t *testing.T) {
	h := newHarness(t, false)
	notebook := strings.Repeat("阿波罗登月计划", 27) + "：首次载人登月任务" // 198 characters
	if _, err := h.pool.Exec(context.Background(), `UPDATE notebooks SET title = $1 WHERE id = 10`, notebook); err != nil {
		t.Fatal(err)
	}

	status, body := h.as(alice, "POST", generate, `{"kind":"mindmap","title":"思维导图: `+notebook+`","request_id":"long1"}`)
	expect(t, "a mind map in a long-titled notebook", status, body, 201, `"kind":"mindmap"`)
	h.until("the run finishes", "deliverable_jobs WHERE kind = 'artifact' AND status = 'ready'")
	var listed string
	if err := h.pool.QueryRow(context.Background(), `SELECT title FROM notebook_outputs WHERE notebook_id = 10`).Scan(&listed); err != nil {
		t.Fatal(err)
	}
	if utf8.RuneCountInString(listed) != maxTitleChars || !strings.HasPrefix(listed, "思维导图: 阿波罗") || !utf8.ValidString(listed) {
		t.Errorf("listed as %q", listed)
	}
	task := h.model.requests[0].Messages[len(h.model.requests[0].Messages)-1].Content
	if !strings.Contains(task, `Title: "`+notebook+`"`) || strings.Contains(task, "思维导图") {
		t.Error("the mind map should be headed by the whole notebook title")
	}

	// And a video, asked by an installed phone in English.
	status, body = h.as(alice, "POST", generate, `{"kind":"video","title":"Video Overview: `+notebook+`","request_id":"long2"}`)
	expect(t, "a video in a long-titled notebook", status, body, 201, `"kind":"video"`)
	if len(h.videos.briefs) != 1 || h.videos.briefs[0].Title != notebook {
		t.Errorf("the video was queued as %+v", h.videos.briefs)
	}
}
