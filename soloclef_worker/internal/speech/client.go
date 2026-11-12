package speech

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/audiofile"
)

// Audio is what Client needs done to the bytes a provider sends
// (audiofile.Tools).
type Audio interface {
	Seconds(ctx context.Context, data []byte) (float64, error)
	ToMP3(ctx context.Context, data []byte) ([]byte, error)
}

// Client voices one line at a time through the proxy.
type Client struct {
	// BaseURL is the proxy's OpenAI-style root, ending in /v1.
	BaseURL string
	// Key is sent as a bearer token.
	Key     string
	Service Service
	HTTP    *http.Client
	Audio   Audio
	// AttemptTimeout bounds one request. The provider behind the proxy may
	// not answer at all (Kokoro at OpenRouter has hung for hours), and the
	// render waits on every line, so an unbounded attempt does not fail a
	// job: it holds the queue.
	AttemptTimeout time.Duration
	// MaxAttempts is how many requests one line may take. Only a failure
	// that may pass is retried: a refused voice or an unknown model fails
	// on the first.
	MaxAttempts int
	// Backoff is the wait before the second attempt; the wait grows by the
	// same step each time.
	Backoff time.Duration
	Log     *slog.Logger
}

// Check reports what is missing. Called once at start-up.
func (c Client) Check() error {
	switch {
	case c.BaseURL == "" || c.Key == "":
		return errors.New("speech: the proxy's address or key is not set")
	case c.Service.Model == "":
		return errors.New("speech: no speech service")
	case c.HTTP == nil || c.Audio == nil || c.Log == nil:
		return errors.New("speech: a dependency is missing")
	case c.AttemptTimeout <= 0 || c.MaxAttempts < 1 || c.Backoff < 0:
		return errors.New("speech: the attempt policy is incomplete")
	}
	return nil
}

// Line is one thing to say.
type Line struct {
	Text string
	// Voice is the service's reference (Service.Voice).
	Voice any
	// Instructions is the speaking style, or "". It is sent only to a model
	// that takes one.
	Instructions string
}

// minimumAudible is shorter than any spoken line. An answer holding less is
// a transfer that stopped partway or an error page read as samples: well
// formed, silent, and a slide of silence in the finished episode if it were
// accepted.
const minimumAudible = 0.05

// maxAnswerBytes bounds one line's audio. A long line of dialogue is a few
// hundred kilobytes of MP3.
const maxAnswerBytes = 32 << 20

// transientStatus are the answers that may pass: capacity, rate limits,
// gateway faults.
var transientStatus = map[int]bool{408: true, 409: true, 425: true, 429: true, 500: true, 502: true, 503: true, 504: true, 529: true}

// failure is one attempt's failure, and whether another could answer
// differently.
type failure struct {
	err       error
	transient bool
}

// Speak returns the line as MP3 bytes.
func (c Client) Speak(ctx context.Context, line Line) ([]byte, error) {
	var last error
	for attempt := 1; attempt <= c.MaxAttempts; attempt++ {
		if attempt > 1 {
			select {
			case <-time.After(c.Backoff * time.Duration(attempt-1)):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		audio, problem := c.attempt(ctx, line)
		if problem == nil {
			return audio, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !problem.transient {
			return nil, fmt.Errorf("speech: %s refused the line: %w", c.Service.Model, problem.err)
		}
		c.Log.Info("a line's synthesis failed and will be tried again",
			"model", c.Service.Model, "attempt", attempt, "of", c.MaxAttempts, "error", problem.err.Error())
		last = problem.err
	}
	return nil, fmt.Errorf("speech: %s failed %d times: %w", c.Service.Model, c.MaxAttempts, last)
}

func (c Client) attempt(ctx context.Context, line Line) ([]byte, *failure) {
	ctx, cancel := context.WithTimeout(ctx, c.AttemptTimeout)
	defer cancel()
	request := map[string]any{"model": c.Service.Model, "voice": line.Voice, "input": line.Text, "response_format": "mp3"}
	if line.Instructions != "" && c.Service.TakesInstructions() {
		request["instructions"] = line.Instructions
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, &failure{err: err}
	}
	call, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/audio/speech", bytes.NewReader(body))
	if err != nil {
		return nil, &failure{err: err}
	}
	call.Header.Set("Content-Type", "application/json")
	call.Header.Set("Authorization", "Bearer "+c.Key)
	response, err := c.HTTP.Do(call)
	if err != nil {
		// No answer at all, a deadline included: the next attempt may get
		// one.
		return nil, &failure{err: err, transient: true}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxAnswerBytes+1))
	if err != nil {
		return nil, &failure{err: fmt.Errorf("reading the answer: %w", err), transient: true}
	}
	if response.StatusCode != http.StatusOK {
		explanation := strings.TrimSpace(string(data))
		if len(explanation) > 300 {
			explanation = explanation[:300]
		}
		return nil, &failure{err: fmt.Errorf("answered %d: %s", response.StatusCode, explanation), transient: transientStatus[response.StatusCode]}
	}
	if len(data) > maxAnswerBytes {
		return nil, &failure{err: fmt.Errorf("answered more than %d bytes for one line", maxAnswerBytes)}
	}
	if len(data) == 0 {
		return nil, &failure{err: errors.New("answered no audio"), transient: true}
	}
	// One ffmpeg process a line: the answer is made MP3 in the episode's
	// format, and the MP3 is measured from its frames. Bare samples are
	// measured before, by arithmetic, because an encoder pads what it is
	// given and would hide how little there was.
	if !audiofile.IsMP3(data) && !audiofile.HasContainer(data) {
		if seconds, _ := c.Audio.Seconds(ctx, data); seconds < minimumAudible {
			return nil, &failure{err: fmt.Errorf("answered %d bytes holding %.3fs of sound, which is not a spoken line", len(data), seconds), transient: true}
		}
	}
	mp3, err := c.Audio.ToMP3(ctx, data)
	if err != nil {
		// Bytes that do not decode are a transfer that went wrong, or a
		// page that is not audio: worth another attempt.
		return nil, &failure{err: err, transient: true}
	}
	seconds, err := c.Audio.Seconds(ctx, mp3)
	if err != nil {
		return nil, &failure{err: err, transient: true}
	}
	if seconds < minimumAudible {
		return nil, &failure{err: fmt.Errorf("answered %d bytes holding %.3fs of sound, which is not a spoken line", len(data), seconds), transient: true}
	}
	return mp3, nil
}
