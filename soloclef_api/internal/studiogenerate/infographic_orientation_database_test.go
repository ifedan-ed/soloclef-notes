package studiogenerate

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"strings"
	"sync"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/deliverables"
)

// painter is the image model of the harness's specialist: it records what
// it was asked for and draws a plain page.
type painter struct {
	mu      sync.Mutex
	sizes   []string
	prompts []string
}

func (p *painter) Check() error { return nil }
func (p *painter) Name() string { return "test-image-model" }
func (p *painter) Generate(_ context.Context, prompt, size string) (deliverables.GeneratedImage, error) {
	p.mu.Lock()
	p.sizes, p.prompts = append(p.sizes, size), append(p.prompts, prompt)
	p.mu.Unlock()
	page := image.NewNRGBA(image.Rect(0, 0, 384, 256))
	for i := 0; i < len(page.Pix); i += 4 {
		page.Pix[i], page.Pix[i+1], page.Pix[i+2], page.Pix[i+3] = 20, 90, 160, 255
	}
	var out bytes.Buffer
	if err := png.Encode(&out, page); err != nil {
		return deliverables.GeneratedImage{}, err
	}
	return deliverables.GeneratedImage{Data: out.Bytes(), MimeType: "image/png", Extension: "png"}, nil
}

func (p *painter) asked() ([]string, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.sizes...), append([]string(nil), p.prompts...)
}

// infographicPlan is the plan the scripted specialist saves, as a JSON
// string: a title and three sections, enough to be an infographic.
var infographicPlan = func() string {
	plan, _ := json.Marshal("# Tides\n\n## The moon\n- Pulls the sea\n\n## The sun\n- Pulls less\n\n## Spring tides\n- Both in line\n")
	return string(plan)
}()

// The orientation chosen in the dialog is the canvas the infographic is
// drawn on: the image model is asked for that size, and the style the
// dialog chose answers the run's style question beside it.
func TestAnInfographicIsDrawnInTheDialogsOrientation(t *testing.T) {
	h := newHarness(t, false)

	status, body := h.as(alice, "POST", generate, `{"kind":"infographic","style":"scientific","orientation":"portrait","request_id":"i1"}`)
	expect(t, "her portrait infographic", status, body, 201, `"kind":"infographic"`, `"orientation":"portrait"`, `"style":"scientific"`)
	h.until("the run finishes", "deliverable_jobs WHERE kind = 'artifact' AND status = 'ready' AND artifact_id IS NOT NULL")
	sizes, prompts := h.painter.asked()
	if len(sizes) != 1 || sizes[0] != "1024x1536" {
		t.Fatalf("the image model was asked for %v", sizes)
	}
	if !strings.Contains(prompts[0], "Canvas: portrait") || !strings.Contains(prompts[0], "VISUAL STYLE") {
		t.Errorf("the prompt does not describe the chosen canvas: %q", prompts[0])
	}
	if h.count(`artifacts WHERE user_id = '`+alice+`' AND format = 'infographic'
		AND metadata #>> '{generation,orientation}' = 'portrait' AND metadata #>> '{generation,requested_style_id}' = 'scientific'`) != 1 {
		t.Error("the infographic should be saved with the orientation and style the dialog chose")
	}

	// No orientation sent is the dialog's default, landscape.
	status, body = h.as(alice, "POST", generate, `{"kind":"infographic","request_id":"i2"}`)
	expect(t, "her default infographic", status, body, 201, `"orientation":"landscape"`)
	h.until("the second run finishes", "(SELECT 1 FROM deliverable_jobs WHERE kind = 'artifact' AND status = 'ready' HAVING count(*) = 2) AS finished")
	if sizes, _ := h.painter.asked(); len(sizes) != 2 || sizes[1] != "1536x1024" {
		t.Errorf("the default orientation asked for %v", sizes)
	}
}
