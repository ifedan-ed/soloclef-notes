package videojob

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_studio/sandbox"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/speech"
)

// The settings. Every one is required when the render is enabled, except
// the narration's speaking style, which may be empty; none has a default,
// for the reason internal/workerconfig gives.
const (
	// Enabled is "true" or "false". False keeps the job that marks every
	// queued video failed at once (refusal.go), for a deployment with no
	// sandbox; it must be said, not inferred from a missing setting.
	Enabled = "SOLOCLEF_WORKER_VIDEO_ENABLED"

	// The OpenSandbox server, as the API tier's SOLOCLEF_API_SANDBOX_*
	// name it, and the size of one video's sandbox.
	// SandboxStack is the stack's name on its sandboxes, the API's
	// SOLOCLEF_API_SANDBOX_STACK (studio sandbox, "One stack's sandboxes").
	SandboxStack          = "SOLOCLEF_WORKER_VIDEO_SANDBOX_STACK"
	SandboxURL            = "SOLOCLEF_WORKER_VIDEO_SANDBOX_URL"
	SandboxKeyFile        = "SOLOCLEF_WORKER_VIDEO_SANDBOX_API_KEY_FILE"
	SandboxImage          = "SOLOCLEF_WORKER_VIDEO_SANDBOX_IMAGE"
	SandboxCPU            = "SOLOCLEF_WORKER_VIDEO_SANDBOX_CPU"
	SandboxMemory         = "SOLOCLEF_WORKER_VIDEO_SANDBOX_MEMORY"
	SandboxIdleTTL        = "SOLOCLEF_WORKER_VIDEO_SANDBOX_IDLE_TTL"
	SandboxCommandTimeout = "SOLOCLEF_WORKER_VIDEO_SANDBOX_COMMAND_TIMEOUT"

	// RenderTimeout bounds one long sandbox command: the stills, the scene
	// parts, the full render.
	RenderTimeout = "SOLOCLEF_WORKER_VIDEO_RENDER_TIMEOUT"
	// RenderMode is frames (Remotion renders every frame) or stills (each
	// scene's entrance is rendered and its last still held to the
	// narration; app/deliverables/video/render_mode.py).
	RenderMode = "SOLOCLEF_WORKER_VIDEO_RENDER_MODE"
	// The harness's own knobs (docker/sandbox/remotion/render.mjs): frames
	// rendered at once (the sandbox's CPU count), the frames of one encoded
	// segment, and how long one frame may take.
	RenderConcurrency = "SOLOCLEF_WORKER_VIDEO_RENDER_CONCURRENCY"
	FramesPerSegment  = "SOLOCLEF_WORKER_VIDEO_FRAMES_PER_SEGMENT"
	FrameTimeout      = "SOLOCLEF_WORKER_VIDEO_FRAME_TIMEOUT"
	// MaxRenders is how many renders this worker runs at once. A render
	// takes its sandbox's every core, so this is a statement about the
	// host, not about the queue.
	MaxRenders = "SOLOCLEF_WORKER_VIDEO_MAX_RENDERS"

	// NarrationVoice is the voice id the narration is spoken in
	// ("openai:marin"), of the roster SOLOCLEF_WORKER_TTS_SERVICE speaks.
	NarrationVoice = "SOLOCLEF_WORKER_VIDEO_NARRATION_VOICE"
	// NarrationInstructions is the speaking style, for a model that takes
	// one. May be empty.
	NarrationInstructions = "SOLOCLEF_WORKER_VIDEO_NARRATION_INSTRUCTIONS"
)

// The two ways a video is assembled.
const (
	ModeFrames = "frames"
	ModeStills = "stills"
)

// minFrameTimeout is the harness's own floor: it refuses to start below it.
const minFrameTimeout = 7 * time.Second

// Config is the video settings, read and checked.
type Config struct {
	Enabled bool

	Sandbox       sandbox.Settings
	RenderTimeout time.Duration
	Mode          string

	RenderConcurrency int
	FramesPerSegment  int
	FrameTimeout      time.Duration
	MaxRenders        int

	// Voice is what the speech service is sent as `voice`; VoiceID is the
	// setting it was read from.
	Voice                 any
	VoiceID               string
	NarrationInstructions string
}

// LoadConfig reads the settings, or says everything that is wrong with
// them. The narration voice is checked against the speech service the
// worker speaks with, so a voice of another roster stops the worker here
// rather than every video later.
func LoadConfig(getenv func(string) string, service speech.Service) (Config, error) {
	value := func(name string) string { return strings.TrimSpace(getenv(name)) }
	switch value(Enabled) {
	case "false":
		return Config{}, nil
	case "true":
	default:
		return Config{}, fmt.Errorf("%s must be true or false; got %q", Enabled, value(Enabled))
	}

	var problems []string
	required := func(name string) string {
		found := value(name)
		if found == "" {
			problems = append(problems, name+" is not set")
		}
		return found
	}
	duration := func(name string) time.Duration {
		raw := required(name)
		if raw == "" {
			return 0
		}
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			problems = append(problems, fmt.Sprintf("%s=%q is not a duration greater than zero (for example 90s)", name, raw))
		}
		return parsed
	}
	count := func(name string) int {
		raw := required(name)
		if raw == "" {
			return 0
		}
		number, err := strconv.Atoi(raw)
		if err != nil || number < 1 {
			problems = append(problems, fmt.Sprintf("%s=%q is not a whole number of at least 1", name, raw))
		}
		return number
	}
	key := ""
	if path := required(SandboxKeyFile); path != "" {
		content, err := os.ReadFile(path)
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("%s names %q, which cannot be read: %v", SandboxKeyFile, path, err))
		case strings.TrimSpace(string(content)) == "":
			problems = append(problems, fmt.Sprintf("%s names %q, which is empty", SandboxKeyFile, path))
		default:
			key = strings.TrimSpace(string(content))
		}
	}

	config := Config{
		Enabled: true,
		Sandbox: sandbox.Settings{
			Stack: required(SandboxStack), URL: required(SandboxURL), APIKey: key, Image: required(SandboxImage),
			CPU: required(SandboxCPU), Memory: required(SandboxMemory),
			IdleTTL: duration(SandboxIdleTTL), CommandTimeout: duration(SandboxCommandTimeout),
			// A job's sandbox is not counted against the account's limit
			// (studio sandbox/job.go), so the limit means nothing here; the
			// client still wants one.
			MaxPerAccount: 1,
		},
		RenderTimeout:         duration(RenderTimeout),
		Mode:                  required(RenderMode),
		RenderConcurrency:     count(RenderConcurrency),
		FramesPerSegment:      count(FramesPerSegment),
		FrameTimeout:          duration(FrameTimeout),
		MaxRenders:            count(MaxRenders),
		VoiceID:               required(NarrationVoice),
		NarrationInstructions: value(NarrationInstructions),
	}
	if config.Mode != "" && config.Mode != ModeFrames && config.Mode != ModeStills {
		problems = append(problems, fmt.Sprintf("%s=%q must be %s or %s", RenderMode, config.Mode, ModeFrames, ModeStills))
	}
	if config.FrameTimeout > 0 && config.FrameTimeout < minFrameTimeout {
		problems = append(problems, fmt.Sprintf("%s must be at least %s: the harness refuses less", FrameTimeout, minFrameTimeout))
	}
	if config.FrameTimeout%time.Millisecond != 0 {
		problems = append(problems, FrameTimeout+" must be a whole number of milliseconds")
	}
	if config.Sandbox.IdleTTL > 0 && config.Sandbox.IdleTTL < time.Minute {
		problems = append(problems, SandboxIdleTTL+" must be at least a minute: the server refuses less")
	}
	if config.VoiceID != "" {
		voice, err := service.Voice(config.VoiceID)
		if err != nil {
			problems = append(problems, NarrationVoice+": "+err.Error())
		}
		config.Voice = voice
	}
	if len(problems) > 0 {
		return Config{}, errors.New("the video settings are incomplete:\n  " + strings.Join(problems, "\n  "))
	}
	return config, nil
}
