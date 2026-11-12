package videojob

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"time"
)

// harness runs the Remotion harness the sandbox image carries at
// /opt/remotion (docker/sandbox/remotion/render.mjs) in one attempt's
// working directory.
type harness struct {
	box     Sandbox
	workdir string
	config  Config
	// short bounds an ordinary command; long a render.
	short, long time.Duration
	// renders is the worker's gate on renders at once (Job.renders).
	renders chan struct{}
}

// harnessRoot is where the image keeps the harness.
const harnessRoot = "/opt/remotion"

// maxOutputTail is how much of a failing command's output is kept: what a
// repair is shown, and what the row's diagnostic is cut from.
const maxOutputTail = 16_000

// The frame grid of every video. Thirty frames a second is what the
// harness's composition declares; a scene lasts at least one second.
const (
	fps       = 30
	minFrames = 30
)

// prepare makes the working directory a fresh copy of the harness. A
// takeover finds the dead attempt's files; they are removed first.
func (h harness) prepare(ctx context.Context) error {
	dir := quote(h.workdir)
	return h.checked(ctx, "rm -rf -- "+dir+" && mkdir -p -- "+dir+" && cp -a "+harnessRoot+"/. "+dir+"/", h.short, "prepare the Remotion working directory")
}

// checked runs a command that must succeed, as an error that says what it
// was doing and how the command ended.
func (h harness) checked(ctx context.Context, command string, limit time.Duration, doing string) error {
	result, err := h.box.RunWithin(ctx, command, limit)
	if err != nil {
		return fmt.Errorf("could not %s: %w", doing, err)
	}
	if !result.OK() {
		ending := "exited " + strconv.Itoa(result.ExitCode)
		if result.TimedOut {
			ending = "was stopped at its time limit of " + limit.String()
		}
		return fmt.Errorf("could not %s: the command %s: %s", doing, ending, tail(result.Output, maxOutputTail))
	}
	return nil
}

// environment is the harness's knobs, set for a render.mjs command.
func (h harness) environment() string {
	return "export VIDEO_SANDBOX_MAX_FRAMES_PER_SEGMENT=" + strconv.Itoa(h.config.FramesPerSegment) +
		" VIDEO_SANDBOX_RENDER_FRAME_TIMEOUT_MS=" + strconv.FormatInt(h.config.FrameTimeout.Milliseconds(), 10) +
		" VIDEO_SANDBOX_RENDER_CONCURRENCY=" + strconv.Itoa(h.config.RenderConcurrency) + "; "
}

// node runs render.mjs with the arguments, in the working directory.
func (h harness) node(arguments ...string) string {
	command := h.environment() + "cd -- " + quote(h.workdir) + " && node render.mjs"
	for _, argument := range arguments {
		command += " " + quote(argument)
	}
	return command
}

// project is the props the harness renders: the scenes' code and each
// scene's narration, by its name under public/.
type project struct {
	FPS       int            `json:"fps"`
	MinFrames int            `json:"min_duration_in_frames"`
	Scenes    []projectScene `json:"scenes"`
}

type projectScene struct {
	Number   int    `json:"slide_number"`
	Filename string `json:"filename"`
	Code     string `json:"code"`
	Audio    string `json:"audio"`
}

// audioName is a scene's narration file, under public/.
func audioName(number int) string { return "slide-" + strconv.Itoa(number) + ".mp3" }

// writeProps writes the scenes for the harness and returns the file's path.
func (h harness) writeProps(ctx context.Context, video Authored) (string, error) {
	props := project{FPS: fps, MinFrames: minFrames}
	for _, scene := range video.Scenes {
		props.Scenes = append(props.Scenes, projectScene{Number: scene.Number, Filename: scene.Filename, Code: scene.Code, Audio: audioName(scene.Number)})
	}
	data, err := json.Marshal(props)
	if err != nil {
		return "", err
	}
	where := path.Join(h.workdir, "props.json")
	if err := h.box.WriteFile(ctx, where, data); err != nil {
		return "", fmt.Errorf("could not write the scenes into the sandbox: %w", err)
	}
	return where, nil
}

// writeNarration puts each scene's voice where the props name it.
func (h harness) writeNarration(ctx context.Context, voices []voiced) error {
	for _, voice := range voices {
		if err := h.box.WriteFile(ctx, path.Join(h.workdir, "public", audioName(voice.Number)), voice.MP3); err != nil {
			return fmt.Errorf("could not write the narration of scene %d into the sandbox: %w", voice.Number, err)
		}
	}
	return nil
}

// issue runs a check whose failure is the scenes' fault: the build of the
// TSX, a still that will not render. What it printed is the issue, to be
// shown to a repair; an error is the sandbox itself failing.
func (h harness) issue(ctx context.Context, command string, limit time.Duration) (string, error) {
	result, err := h.box.RunWithin(ctx, command, limit)
	if err != nil {
		return "", err
	}
	if result.OK() {
		return "", nil
	}
	if result.TimedOut {
		return "the check was stopped at its time limit of " + limit.String() + ": " + tail(result.Output, maxOutputTail), nil
	}
	if result.Output == "" {
		return "the check exited " + strconv.Itoa(result.ExitCode) + " and printed nothing", nil
	}
	return tail(result.Output, maxOutputTail), nil
}

// preflight builds the scenes and measures the composition. It takes
// seconds and is not a render, so it does not wait for the render gate (the
// Python gate held it behind any render running in the process).
func (h harness) preflight(ctx context.Context, props string) (string, error) {
	return h.issue(ctx, h.node("--preflight", props), h.short)
}

// stillsDir is where the layout check's stills and its report are
// written.
func (h harness) stillsDir() string { return path.Join(h.workdir, "stills") }

// stills renders each scene's last frame and measures its layout, writing
// layout.json beside the stills (layout.go reads it).
func (h harness) stills(ctx context.Context, props string) (string, error) {
	return h.issue(ctx, h.node("--stills", props, h.stillsDir()), h.long)
}

// gated runs work holding one of the worker's render slots. The wait ends
// with the job: a cancelled video does not wait for a slot to stop.
func (h harness) gated(ctx context.Context, work func() error) error {
	select {
	case h.renders <- struct{}{}:
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	defer func() { <-h.renders }()
	return work()
}

// renderFrames renders every frame to output.
func (h harness) renderFrames(ctx context.Context, props, output string) error {
	return h.gated(ctx, func() error {
		return h.checked(ctx, h.node(props, output), h.long, "render the video")
	})
}
