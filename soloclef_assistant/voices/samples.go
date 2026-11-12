package voices

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Speaker turns a sentence into audio. Proxy is the real one; a test names
// another, because the real one costs money.
type Speaker interface {
	Speak(ctx context.Context, model string, voice any, text string) ([]byte, error)
}

// SampleStore is where a synthesised sample is kept, for every replica of
// this tier to find. ValkeySamples is the real one.
type SampleStore interface {
	// Sample is the audio kept under the key, and whether there is any.
	Sample(ctx context.Context, key string) (audio []byte, found bool, err error)
	// Keep stores the audio under the key.
	Keep(ctx context.Context, key string, audio []byte) error
}

// Sample is a few seconds of a voice, ready to be played.
type Sample struct {
	Audio []byte
	// ContentType is read off the audio itself (container).
	ContentType string
}

// sampleTexts is the sentence a voice says, by the primary subtag of the
// voice's language: a voice is judged best speaking its own. A voice that
// speaks any language says the English one.
//
// Every language a roster has a voice for has a sentence here, and
// TestEveryVoiceHasASentenceInItsOwnLanguage holds that. So there is no
// "otherwise, English": a voice with no sentence is an error, not a French
// voice reading English.
var sampleTexts = map[string]string{
	"en": "Hi there! This is how I sound when narrating your podcast.",
	"es": "¡Hola! Así sueno cuando narro tu pódcast.",
	"fr": "Bonjour ! Voici ma voix quand je raconte votre podcast.",
	"hi": "नमस्ते! आपका पॉडकास्ट सुनाते समय मेरी आवाज़ ऐसी होती है।",
	"it": "Ciao! Questa è la mia voce quando racconto il tuo podcast.",
	"ja": "こんにちは。ポッドキャストをお届けするときの私の声です。",
	"pt": "Olá! É assim que eu soo ao narrar o seu podcast.",
	"zh": "你好！这就是我为你播报播客时的声音。",
}

// sampleLanguage is the language a voice's sample is spoken in.
func sampleLanguage(voice Voice) string {
	if voice.Language == AnyLanguage {
		return "en"
	}
	return voice.Language
}

const (
	// speakTimeout bounds one synthesis. A sentence takes a second or two;
	// a provider that has stopped answering must not hold a request open.
	speakTimeout = 60 * time.Second
	// sampleRetryAfter is how long a failed synthesis is remembered, per
	// voice. A person pressing play again and again on a voice whose
	// provider is failing is answered from memory, not with another call.
	sampleRetryAfter = 30 * time.Second
)

// ErrStoreUnavailable is the sample store not answering. No sample is made
// in that case -- see Samples.
var ErrStoreUnavailable = errors.New("the voice sample store could not be read")

// Samples makes and keeps the audible sample of each voice.
//
// # Why this cannot be used as a free speech service
//
// The route that serves a sample calls a paid provider. Three things bound
// what that can cost, and none of them is a rate limit:
//
//   - The caller chooses nothing but the voice. The sentence is fixed here,
//     the model is the deployment's, and the voice must be one the roster
//     has. Nobody's text is ever spoken.
//   - Each voice is synthesised once and kept in the store, which every
//     replica shares, for sampleLifetime. So the most a deployment can spend
//     is one sentence per voice per month, whoever asks and however often.
//   - If the store cannot be read, no sample is made. Synthesising without
//     the store would be an uncounted call per request, which is exactly
//     the hole this closes; a few minutes without voice samples is cheap.
//
// Requests for the same voice that arrive together on one replica make one
// call between them (flights). Two replicas asked for the same never-heard
// voice in the same second can each make one; that is at most one extra
// sentence per replica per voice per month, and a lock across replicas to
// prevent it would cost more to get right than it could ever save.
type Samples struct {
	service Service
	speaker Speaker
	store   SampleStore
	logger  *slog.Logger
	now     func() time.Time

	mutex sync.Mutex
	// flights is the samples being fetched now, by key.
	flights map[string]*flight
	// failures is when and why a voice last failed to synthesise, by key.
	failures map[string]failure
}

type flight struct {
	done   chan struct{}
	sample Sample
	err    error
}

type failure struct {
	at  time.Time
	err error
}

// NewSamples is the samples of the catalogue's service.
func NewSamples(catalogue *Catalogue, speaker Speaker, store SampleStore, logger *slog.Logger) *Samples {
	service, _ := catalogue.Service()
	return &Samples{
		service: service, speaker: speaker, store: store, logger: logger, now: time.Now,
		flights: map[string]*flight{}, failures: map[string]failure{},
	}
}

// sampleKey names a sample by everything that decides how it sounds: the
// model, the voice, the language and the sentence. Re-pointing the service
// at another model, or rewording a sentence, therefore starts afresh rather
// than playing a sample of something else.
func sampleKey(model string, voice Voice, language, text string) (string, error) {
	material, err := json.Marshal([]any{model, voice.Native, language, text})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(material)
	return hex.EncodeToString(sum[:]), nil
}

// Of is the sample of a voice: from the store if it is there, and otherwise
// synthesised, kept, and answered.
func (s *Samples) Of(ctx context.Context, voice Voice) (Sample, error) {
	language := sampleLanguage(voice)
	text, written := sampleTexts[primary(language)]
	if !written {
		return Sample{}, fmt.Errorf("no sample sentence is written for the language %q of voice %s", language, voice.ID)
	}
	key, err := sampleKey(s.service.Model, voice, language, text)
	if err != nil {
		return Sample{}, err
	}

	s.mutex.Lock()
	if flying, underway := s.flights[key]; underway {
		// Somebody is already fetching this voice; take their answer.
		s.mutex.Unlock()
		select {
		case <-flying.done:
			return flying.sample, flying.err
		case <-ctx.Done():
			return Sample{}, ctx.Err()
		}
	}
	flying := &flight{done: make(chan struct{})}
	s.flights[key] = flying
	s.mutex.Unlock()

	flying.sample, flying.err = s.fetch(ctx, key, voice, text)

	s.mutex.Lock()
	delete(s.flights, key)
	s.mutex.Unlock()
	close(flying.done)
	return flying.sample, flying.err
}

func (s *Samples) fetch(ctx context.Context, key string, voice Voice, text string) (Sample, error) {
	kept, found, err := s.store.Sample(ctx, key)
	if err != nil {
		return Sample{}, fmt.Errorf("%w: %w", ErrStoreUnavailable, err)
	}
	if found {
		return playable(kept)
	}

	s.mutex.Lock()
	last, failed := s.failures[key]
	s.mutex.Unlock()
	if failed && s.now().Sub(last.at) < sampleRetryAfter {
		return Sample{}, last.err
	}

	// Detached from the request: once the call is made it is paid for, and
	// the listener closing the dialog must not throw the audio away before
	// it is kept for the next one.
	speaking, cancel := context.WithTimeout(context.WithoutCancel(ctx), speakTimeout)
	defer cancel()
	audio, err := s.speaker.Speak(speaking, s.service.Model, voice.Native, text)
	if err != nil {
		return Sample{}, s.remember(key, fmt.Errorf("the speech service did not voice a sample of %s: %w", voice.ID, err))
	}
	sample, err := playable(audio)
	if err != nil {
		return Sample{}, s.remember(key, fmt.Errorf("the speech service's sample of %s cannot be played: %w", voice.ID, err))
	}
	if err := s.store.Keep(speaking, key, sample.Audio); err != nil {
		// The listener still gets the sample that was just paid for. But a
		// store that takes no writes would make every request a paid call,
		// so the failure is remembered like any other and the next request
		// for this voice within sampleRetryAfter makes none.
		s.remember(key, fmt.Errorf("%w: a sample of %s could not be kept: %w", ErrStoreUnavailable, voice.ID, err))
		s.logger.LogAttrs(ctx, slog.LevelError, "a voice sample was synthesised and could not be kept",
			slog.String("voice", voice.ID), slog.String("error", err.Error()))
	}
	return sample, nil
}

func (s *Samples) remember(key string, err error) error {
	s.mutex.Lock()
	s.failures[key] = failure{at: s.now(), err: err}
	s.mutex.Unlock()
	return err
}
