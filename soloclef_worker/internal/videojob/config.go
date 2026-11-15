package videojob

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/speech"
)

// The settings. Every one is required when the video is enabled, except
// the narration's speaking style, which may be empty; none has a default,
// for the reason internal/workerconfig gives.
const (
	// Enabled is "true" or "false". False keeps the job that marks every
	// queued video failed at once (refusal.go), for a deployment that
	// makes none; it must be said, not inferred from a missing setting.
	Enabled = "SOLOCLEF_WORKER_VIDEO_ENABLED"

	// Resvg is the resvg program the frames are drawn with (the worker
	// image has it at /usr/local/bin/resvg).
	Resvg = "SOLOCLEF_WORKER_VIDEO_RESVG"
	// FrameWorkers is how many frames are drawn at once: the cores one
	// video may take while its frames are drawn.
	FrameWorkers = "SOLOCLEF_WORKER_VIDEO_FRAME_WORKERS"
	// RenderTimeout bounds drawing and joining one video's frames.
	RenderTimeout = "SOLOCLEF_WORKER_VIDEO_RENDER_TIMEOUT"
	// MaxRenders is how many videos this worker draws at once. Each takes
	// FrameWorkers cores, so this is a statement about the host, not about
	// the queue.
	MaxRenders = "SOLOCLEF_WORKER_VIDEO_MAX_RENDERS"

	// NarrationVoice is the voice id the narration is spoken in
	// ("openai:marin"), of the roster SOLOCLEF_WORKER_TTS_SERVICE speaks.
	NarrationVoice = "SOLOCLEF_WORKER_VIDEO_NARRATION_VOICE"
	// NarrationInstructions is the speaking style, for a model that takes
	// one. May be empty.
	NarrationInstructions = "SOLOCLEF_WORKER_VIDEO_NARRATION_INSTRUCTIONS"
)

// The image's own programs. They are part of the worker image, not of a
// deployment, so they are not settings; Check runs each once at start-up.
const (
	ffmpegProgram  = "ffmpeg"
	ffprobeProgram = "ffprobe"
)

// Config is the video settings, read and checked.
type Config struct {
	Enabled bool

	Resvg         string
	FrameWorkers  int
	RenderTimeout time.Duration
	MaxRenders    int

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
			problems = append(problems, fmt.Sprintf("%s=%q is not a duration greater than zero (for example 10m)", name, raw))
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
	config := Config{
		Enabled:               true,
		Resvg:                 required(Resvg),
		FrameWorkers:          count(FrameWorkers),
		RenderTimeout:         duration(RenderTimeout),
		MaxRenders:            count(MaxRenders),
		VoiceID:               required(NarrationVoice),
		NarrationInstructions: value(NarrationInstructions),
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
