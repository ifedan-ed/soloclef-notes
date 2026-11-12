package videojob

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// The layout check of the scenes. The harness renders each scene's last
// frame, when everything the scene shows is on screen, and measures it in
// the page Chrome laid out (docker/sandbox/remotion/src/layoutCheck.ts):
// text inside its box and off the frame's edge, no text over text, nothing
// painted over text, nothing under 30 px, WCAG contrast against the
// ground, pictures inside the frame, and a frame that shows something. Its
// findings are written to layout.json beside the stills, one entry per
// scene in playback order, and read here; any finding sends the scenes to
// the repair, with the same budget as every other check.
//
// It replaces the Python worker's still review, which showed three stills
// of every scene and a contact sheet to a vision model. The scenes are
// laid out by Chrome, so the layout is known exactly in the page: a rule
// measures it the same way every time, names the text it means, costs a
// few milliseconds instead of a model call of a minute or two, and does
// not stop working when a provider's account runs out of credit.

// layoutFile is the report the harness writes into the stills directory.
const layoutFile = "layout.json"

// maxLayoutBytes bounds the report read: twelve scenes of at most twelve
// findings of a line each are a few kilobytes.
const maxLayoutBytes = 256 << 10

// maxFindingRunes cuts one finding: it quotes the text it is about, and a
// repair needs the start of that, not a paragraph.
const maxFindingRunes = 240

// layoutReport is layout.json.
type layoutReport struct {
	Scenes []struct {
		Number   int      `json:"slide_number"`
		Findings []string `json:"findings"`
	} `json:"scenes"`
}

// readLayout reads the layout check's findings for the scenes, numbered as
// the props number them, each prefixed with its scene. A report that is
// missing, unreadable or not about exactly these scenes is an error: the
// check did not run, which is never a pass.
func readLayout(ctx context.Context, box Sandbox, stillsDir string, numbers []int) ([]string, error) {
	data, err := box.ReadFile(ctx, path.Join(stillsDir, layoutFile), maxLayoutBytes)
	if err != nil {
		return nil, fmt.Errorf("could not read the layout check's report: %w", err)
	}
	return layoutFindings(data, numbers)
}

// layoutFindings reads a report's findings; readLayout says what it holds
// a report to.
func layoutFindings(data []byte, numbers []int) ([]string, error) {
	var report layoutReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("the layout check's report is not readable: %w", err)
	}
	if len(report.Scenes) != len(numbers) {
		return nil, fmt.Errorf("the layout check reported %d scenes for %d", len(report.Scenes), len(numbers))
	}
	var findings []string
	for i, scene := range report.Scenes {
		if scene.Number != numbers[i] {
			return nil, fmt.Errorf("the layout check's scene %d is numbered %d, not %d", i+1, scene.Number, numbers[i])
		}
		for _, finding := range scene.Findings {
			if finding = strings.TrimSpace(finding); finding != "" {
				findings = append(findings, fmt.Sprintf("scene %d: %s", scene.Number, clipRunes(finding, maxFindingRunes)))
			}
		}
	}
	return findings, nil
}
