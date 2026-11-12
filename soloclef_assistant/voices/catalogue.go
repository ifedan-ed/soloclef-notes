package voices

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// ErrNotConfigured is the answer of a deployment with no speech service.
var ErrNotConfigured = errors.New("no speech service is configured (" + ServiceVariable + ")")

// UnreachableError is the proxy not answering the catalogue's question: it
// is down, slow, or said something that is not an answer. It is the one
// failure here that waiting can fix.
type UnreachableError struct {
	Cause error
}

func (e *UnreachableError) Error() string {
	return "the model proxy could not be asked which voices the speech model has: " + e.Cause.Error()
}

func (e *UnreachableError) Unwrap() error { return e.Cause }

// Lister is the one question the catalogue asks of the proxy: the names of
// the voices a model speaks. It answers *MisconfiguredError when the proxy
// answered and the answer shows a wrong setting, and anything else is taken
// to mean the proxy could not be asked. Proxy is the real one.
type Lister interface {
	ModelVoices(ctx context.Context, model string) ([]string, error)
}

const (
	// rosterLifetime is how long an answer from the proxy is used before it
	// is asked again. Which voices a model speaks changes when an operator
	// re-points the model's name in the proxy, which is rare and deliberate;
	// ten minutes means such a change is picked up without a restart and the
	// proxy is asked six times an hour per replica.
	rosterLifetime = 10 * time.Minute
	// retryAfter is how long a failed ask is remembered. Without it every
	// request during a proxy outage would wait out its own timeout, one
	// after another.
	retryAfter = 15 * time.Second
	// askTimeout bounds one ask. The proxy is on the same network and the
	// answer is a small document.
	askTimeout = 5 * time.Second
)

// Catalogue answers which voices the deployment's speech service has.
//
// # Why the cache is per process
//
// For three of the four rosters there is nothing to cache: the roster is data
// in this package, built once by New. For an OpenAI-style service the roster
// is asked of the proxy and kept for rosterLifetime, in this process and
// nowhere else. It is not put in Redis, on purpose:
//
//   - nothing depends on two replicas agreeing at the same instant. A roster
//     is not state this tier owns; it is a copy of the proxy's, and each
//     replica's copy converges on it within one lifetime;
//   - the ask is one small GET, so sharing it would save six requests an
//     hour per replica and cost a second place for the answer to be stale
//     in, plus a failure of its own when Redis is the thing that is down;
//   - what is paid for -- a synthesised sample -- IS shared across replicas
//     (samples.go), because there a second copy costs money.
type Catalogue struct {
	service    Service
	configured bool
	hostNames  []string
	proxy      Lister
	logger     *slog.Logger
	now        func() time.Time

	// fixed is the roster of a service that has one written down. It is nil
	// only for an OpenAI-style service.
	fixed *Roster

	// The rest is the asked roster, guarded by mutex. The mutex is held
	// across the ask itself, so requests that arrive together make one ask
	// between them and the rest take its answer.
	mutex    sync.Mutex
	asked    *Roster
	askedAt  time.Time
	failure  error
	failedAt time.Time
}

// New checks the settings and builds the catalogue. A setting that is wrong
// is an error here, at startup, where it stops the process: the alternative
// is a deployment that starts cleanly and fails every podcast.
//
// What cannot be checked without the proxy -- that an OpenAI-style model
// really has the configured host voices -- is checked on the first ask and
// on every later one, and reported the same way (Roster).
func New(settings Settings, proxy Lister, logger *slog.Logger) (*Catalogue, error) {
	service, configured, err := settings.validate()
	if err != nil {
		return nil, err
	}
	catalogue := &Catalogue{
		service: service, configured: configured, hostNames: settings.PodcastVoices,
		proxy: proxy, logger: logger, now: time.Now,
	}
	if !configured {
		return catalogue, nil
	}
	var written []Voice
	switch service.Roster {
	case Kokoro:
		written = kokoroVoices
	case Azure:
		written = azureVoices
	case Vertex:
		written = vertexVoices
	case OpenAI:
		// Asked of the proxy, on first use.
		return catalogue, nil
	}
	catalogue.fixed, err = newRoster(service.Roster, written, settings.PodcastVoices)
	if err != nil {
		return nil, err
	}
	return catalogue, nil
}

// Service is the configured speech service, and whether there is one.
func (c *Catalogue) Service() (Service, bool) {
	return c.service, c.configured
}

// Roster is the voices the service has now.
//
// It fails three ways, and a caller should tell them apart:
//
//   - ErrNotConfigured: the deployment has no speech service;
//   - *UnreachableError: the proxy could not be asked. Nothing is guessed in
//     its place. If the proxy answered earlier, that answer goes on being
//     used and the failure is logged -- it is the proxy's own last word, not
//     a substitute for it -- and if it never has, this is the error;
//   - *MisconfiguredError: the proxy answered, and the answer shows a wrong
//     setting: it does not serve the model, lists no voices for it, or the
//     model lacks a configured host. An earlier answer is dropped, because
//     it is now known to be wrong.
func (c *Catalogue) Roster(ctx context.Context) (*Roster, error) {
	if !c.configured {
		return nil, ErrNotConfigured
	}
	if c.fixed != nil {
		return c.fixed, nil
	}

	c.mutex.Lock()
	defer c.mutex.Unlock()
	now := c.now()
	if c.asked != nil && now.Sub(c.askedAt) < rosterLifetime {
		return c.asked, nil
	}
	if c.failure != nil && now.Sub(c.failedAt) < retryAfter {
		return c.afterFailure()
	}

	// Detached from the request that happens to be first: its client hanging
	// up must not become the answer every other waiting request is given.
	asking, cancel := context.WithTimeout(context.WithoutCancel(ctx), askTimeout)
	defer cancel()
	roster, err := c.ask(asking)
	if err == nil {
		c.asked, c.askedAt, c.failure = roster, now, nil
		return roster, nil
	}

	var wrong *MisconfiguredError
	if errors.As(err, &wrong) {
		c.asked = nil
	} else {
		var unreachable *UnreachableError
		if !errors.As(err, &unreachable) {
			err = &UnreachableError{Cause: err}
		}
	}
	c.failure, c.failedAt = err, now
	c.logger.LogAttrs(ctx, slog.LevelError, "the voice catalogue could not be refreshed from the model proxy",
		slog.String("service", c.service.Name),
		slog.Bool("using_the_previous_answer", c.asked != nil),
		slog.String("error", err.Error()))
	return c.afterFailure()
}

// afterFailure is the answer while a failure is remembered: the proxy's
// earlier answer if there is still one, and the failure if not.
func (c *Catalogue) afterFailure() (*Roster, error) {
	if c.asked != nil {
		return c.asked, nil
	}
	return nil, c.failure
}

// ask builds the roster of an OpenAI-style service from what the proxy says
// the model speaks.
func (c *Catalogue) ask(ctx context.Context) (*Roster, error) {
	names, err := c.proxy.ModelVoices(ctx, c.service.Model)
	if err != nil {
		return nil, err
	}
	listed := make(map[string]bool, len(names))
	for _, name := range names {
		listed[name] = true
	}
	// In this package's order, not the proxy's: the picker's order should
	// not change because a proxy's configuration was re-sorted.
	speaking := make([]Voice, 0, len(names))
	for _, voice := range openAIVoices {
		if name, _ := voice.Native.(string); listed[name] {
			speaking = append(speaking, voice)
			delete(listed, name)
		}
	}
	if len(speaking) == 0 {
		return nil, misconfigured(ServiceVariable,
			"is %q, and the proxy lists only voices this catalogue has no entry for (%s). "+
				"If the model is a Kokoro one its name must say so; otherwise the voices need adding to soloclef_assistant/voices",
			c.service.Name, strings.Join(names, ", "))
	}
	if len(listed) > 0 {
		// A voice the model speaks and this package has no name or gender
		// for is not offered. That is said out loud, once per refresh, so a
		// new voice is noticed rather than silently missing.
		unknown := make([]string, 0, len(listed))
		for name := range listed {
			unknown = append(unknown, name)
		}
		c.logger.LogAttrs(ctx, slog.LevelWarn, "the speech model has voices the catalogue has no entry for; they are not offered",
			slog.String("service", c.service.Name),
			slog.String("voices", strings.Join(unknown, ", ")))
	}
	return newRoster(OpenAI, speaking, c.hostNames)
}
