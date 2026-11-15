package studiogenerate

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_studio/posterrender"
)

// posters stands for resvg in the harness's specialist: it records each
// poster it is asked to draw and answers a PNG signature.
type posters struct {
	mu    sync.Mutex
	drawn []posterrender.Drawn
}

func (p *posters) PNG(_ context.Context, d posterrender.Drawn) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.drawn = append(p.drawn, d)
	return []byte("\x89PNG\r\n\x1a\n"), nil
}

func (p *posters) asked() []posterrender.Drawn {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]posterrender.Drawn(nil), p.drawn...)
}

// infographicPoster is the poster the scripted specialist saves: three
// sections, enough to be an infographic.
var infographicPoster = func() string {
	poster, _ := json.Marshal(map[string]any{"sections": []map[string]any{
		{"heading": "The moon", "points": []string{"Pulls the sea"}},
		{"heading": "The sun", "points": []string{"Pulls less"}},
		{"heading": "Spring tides", "points": []string{"Both in line"}},
	}})
	return string(poster)
}()

// The orientation and the style chosen in the dialog are the poster's: it
// is laid out on that canvas in that look, and saved with both.
func TestAnInfographicIsDrawnInTheDialogsOrientation(t *testing.T) {
	h := newHarness(t, false)

	status, body := h.as(alice, "POST", generate, `{"kind":"infographic","style":"scientific","orientation":"portrait","request_id":"i1"}`)
	expect(t, "her portrait infographic", status, body, 201, `"kind":"infographic"`, `"orientation":"portrait"`, `"style":"scientific"`)
	h.until("the run finishes", "deliverable_jobs WHERE kind = 'artifact' AND status = 'ready' AND artifact_id IS NOT NULL")
	drawn := h.posters.asked()
	if len(drawn) != 1 || drawn[0].Orientation != "portrait" || drawn[0].Style != "scientific" || drawn[0].Width != 1024 || drawn[0].Height != 1536 {
		t.Fatalf("the poster was drawn as %+v", drawn)
	}
	if h.count(`artifacts WHERE user_id = '`+alice+`' AND format = 'infographic'
		AND metadata #>> '{generation,orientation}' = 'portrait' AND metadata #>> '{generation,requested_style_id}' = 'scientific'`) != 1 {
		t.Error("the infographic should be saved with the orientation and style the dialog chose")
	}
	// The PNG is its file and the SVG its preview.
	if h.count(`artifact_files WHERE role = 'primary' AND mime_type = 'image/png'`) != 1 || h.count(`artifact_files WHERE role = 'preview' AND mime_type = 'image/svg+xml'`) != 1 {
		t.Error("the infographic should be saved as a PNG with its SVG beside it")
	}

	// No orientation sent is the dialog's default, landscape.
	status, body = h.as(alice, "POST", generate, `{"kind":"infographic","request_id":"i2"}`)
	expect(t, "her default infographic", status, body, 201, `"orientation":"landscape"`)
	h.until("the second run finishes", "(SELECT 1 FROM deliverable_jobs WHERE kind = 'artifact' AND status = 'ready' HAVING count(*) = 2) AS finished")
	if drawn := h.posters.asked(); len(drawn) != 2 || drawn[1].Orientation != "landscape" {
		t.Errorf("the default orientation drew %+v", drawn)
	}

	// A style only the installed phone builds still offer is made in auto.
	status, body = h.as(alice, "POST", generate, `{"kind":"infographic","style":"kawaii","request_id":"i3"}`)
	expect(t, "an installed build's retired style", status, body, 201, `"style":"auto"`)
	h.until("the third run finishes", "(SELECT 1 FROM deliverable_jobs WHERE kind = 'artifact' AND status = 'ready' HAVING count(*) = 3) AS finished")
	if drawn := h.posters.asked(); len(drawn) != 3 || drawn[2].Style != "sketch-note" {
		t.Errorf("a retired style drew %s", fmt.Sprint(drawn[len(drawn)-1].Style))
	}
}
