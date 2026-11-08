package enqueue_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_mail/enqueue"
	"git.danvics.com/danvics/soloclef/soloclef_mail/mailjob"
	"git.danvics.com/danvics/soloclef/soloclef_mail/queuekeys"
)

// Is what this producer pushes what app/mail_queue/enqueue.py pushes?
//
// Not a shape check: the app's own enqueue_email runs in a throwaway container
// of the backend image and queues the message on a throwaway Redis, and the
// entry it wrote is compared with this producer's bytes for the same input.
// The id and the enqueued_at clock are read back out of Python's own entry,
// because job.py binds time.time as the dataclass default factory and there is
// no honest way to pin it from outside; every other byte, including the key the
// entry landed on, is Python's.
//
// Nothing is defaulted, so it never touches the live Redis or the live
// container. The commands are in soloclef_mail/AGENTS.md.

// The id and the clock both sides use. Python's uuid4 is patched to the first
// and time.time to the second, so every byte of the entry is pinned and the
// comparison is of the whole string rather than of a string with holes in it.
const (
	pinnedID         = "ffffffffffffffffffffffffffffffff"
	pinnedEnqueuedAt = 1762608000.123456
)

type interopEnv struct {
	image          string
	redisContainer string
	redisFromImage string
	network        string
}

func interopSettings(t *testing.T) interopEnv {
	t.Helper()
	env := interopEnv{
		image:          os.Getenv("SOLOCLEF_MAIL_TEST_IMAGE"),
		redisContainer: os.Getenv("SOLOCLEF_MAIL_TEST_REDIS_CONTAINER"),
		redisFromImage: os.Getenv("SOLOCLEF_MAIL_TEST_REDIS_FROM_CONTAINER"),
		network:        os.Getenv("SOLOCLEF_MAIL_TEST_NETWORK"),
	}
	if env.image == "" || env.redisContainer == "" || env.redisFromImage == "" || env.network == "" {
		t.Skip("set SOLOCLEF_MAIL_TEST_IMAGE, _REDIS_CONTAINER, _REDIS_FROM_CONTAINER and _NETWORK to compare against Python")
	}
	if strings.Contains(env.redisContainer, "soloclef-redis") {
		t.Fatal("that is the live broker; this test empties the Redis it is given")
	}
	return env
}

type interopCase struct {
	Name    string `json:"name"`
	Message struct {
		To             string `json:"to"`
		Subject        string `json:"subject"`
		TextBody       string `json:"text_body"`
		HTMLBody       string `json:"html_body"`
		Sender         string `json:"sender"`
		ReplyTo        string `json:"reply_to"`
		IdempotencyKey string `json:"idempotency_key"`
		Attachments    []struct {
			Filename      string `json:"filename"`
			ContentBase64 string `json:"content_base64"`
			ContentType   string `json:"content_type"`
		} `json:"attachments"`
	} `json:"message"`
	Job struct {
		Kind     string   `json:"kind"`
		Ref      string   `json:"ref"`
		NotAfter *float64 `json:"not_after"`
	} `json:"job"`
}

func TestWhatPythonPushesIsWhatThisProducerPushes(t *testing.T) {
	env := interopSettings(t)
	raw, err := os.ReadFile("../mailjob/testdata/python_job_bytes.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []interopCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			redisCLI(t, env, "flushdb")
			pythonPush(t, env, c)

			onReady := redisCLI(t, env, "llen", queuekeys.Ready)
			if onReady != "1" {
				t.Fatalf("Python put %s entries on %s, not one", onReady, queuekeys.Ready)
			}
			pythonEntry := redisCLI(t, env, "lindex", queuekeys.Ready, "0")

			var pushed []string
			producer := enqueue.Producer{
				Broker: brokerFunc(func(_ context.Context, key, value string) error {
					if key != queuekeys.Ready {
						t.Errorf("pushed onto %q, Python pushed onto %q", key, queuekeys.Ready)
					}
					pushed = append(pushed, value)
					return nil
				}),
				Clock: func() time.Time { return floatToTime(pinnedEnqueuedAt) },
				NewID: func() string { return pinnedID },
			}
			request := enqueue.Request{
				Message: goMessage(t, c),
				Kind:    c.Job.Kind,
				Ref:     c.Job.Ref,
			}
			if c.Job.NotAfter != nil {
				request.NotAfter = floatToTime(*c.Job.NotAfter)
			}
			if _, err := producer.Queue(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			if len(pushed) != 1 {
				t.Fatalf("one send must be one push, got %d", len(pushed))
			}
			if pushed[0] != pythonEntry {
				t.Errorf("not the bytes Python queued\n go: %s\n py: %s", pushed[0], pythonEntry)
			}
		})
	}
}

func goMessage(t *testing.T, c interopCase) mailjob.Message {
	t.Helper()
	var attachments []mailjob.Attachment
	for _, a := range c.Message.Attachments {
		content, err := base64.StdEncoding.DecodeString(a.ContentBase64)
		if err != nil {
			t.Fatal(err)
		}
		attachments = append(attachments, mailjob.Attachment{
			Filename:    a.Filename,
			Content:     content,
			ContentType: a.ContentType,
		})
	}
	return mailjob.Message{
		To:             c.Message.To,
		Subject:        c.Message.Subject,
		TextBody:       c.Message.TextBody,
		Sender:         c.Message.Sender,
		ReplyTo:        c.Message.ReplyTo,
		HTMLBody:       c.Message.HTMLBody,
		Attachments:    attachments,
		IdempotencyKey: c.Message.IdempotencyKey,
	}
}

// floatToTime is the inverse of the producer's unixFloat: the instant a
// Python timestamp named. It does not go through nanoseconds, because the
// not_after cases are far enough out to overflow an int64 of them.
func floatToTime(seconds float64) time.Time {
	whole := math.Floor(seconds)
	return time.Unix(int64(whole), int64(math.Round((seconds-whole)*1e9)))
}

type brokerFunc func(ctx context.Context, key, value string) error

func (f brokerFunc) LPush(ctx context.Context, key, value string) error {
	return f(ctx, key, value)
}

func pythonPush(t *testing.T, env interopEnv, c interopCase) {
	t.Helper()
	payload := map[string]any{
		"redis_url":   env.redisFromImage,
		"id":          pinnedID,
		"enqueued_at": pinnedEnqueuedAt,
		"kind":        c.Job.Kind,
		"ref":         nullable(c.Job.Ref),
		"not_after":   c.Job.NotAfter,
		"message":     pythonMessage(c),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	script, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("docker", "run", "--rm", "-i",
		"--network", env.network,
		"-e", "PYTHONPATH=/app", "-w", "/app",
		"-v", script+"/../scripts/python_enqueue_push.py:/tmp/push.py:ro",
		env.image, "python", "/tmp/push.py")
	cmd.Stdin = strings.NewReader(string(body))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the app's own enqueue_email did not run: %v\n%s", err, out)
	}
}

func pythonMessage(c interopCase) map[string]any {
	attachments := make([]map[string]any, 0, len(c.Message.Attachments))
	for _, a := range c.Message.Attachments {
		attachments = append(attachments, map[string]any{
			"filename":       a.Filename,
			"content_base64": a.ContentBase64,
			"content_type":   a.ContentType,
		})
	}
	return map[string]any{
		"to":              c.Message.To,
		"subject":         c.Message.Subject,
		"text_body":       c.Message.TextBody,
		"html_body":       nullable(c.Message.HTMLBody),
		"sender":          nullable(c.Message.Sender),
		"reply_to":        nullable(c.Message.ReplyTo),
		"idempotency_key": nullable(c.Message.IdempotencyKey),
		"attachments":     attachments,
	}
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func redisCLI(t *testing.T, env interopEnv, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", append([]string{"exec", env.redisContainer, "redis-cli"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("redis-cli %v: %v\n%s", args, err, out)
	}
	return strings.TrimRight(string(out), "\n")
}
