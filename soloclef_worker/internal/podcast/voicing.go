package podcast

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/speech"
)

// Voicer voices one line (speech.Client).
type Voicer interface {
	Speak(ctx context.Context, line speech.Line) ([]byte, error)
}

// voices turns a brief's lines into MP3 files in a podcast's working
// directory, each line once.
//
// The directory is a cache keyed by everything that decides a line's
// sound: the model, the voice, the language, the style and the words. So a
// line voiced while the transcript was still being drafted is the very file
// the render uses, a render picked up again after the worker restarted
// starts from what was already voiced, and a line said twice ("Right.") is
// voiced once. Within one render, lines that are asked for at the same
// moment are coalesced, so two turns of the same words wait on one request.
//
// A file is written under a temporary name and renamed into place, so a
// crash mid-write never leaves half a line in the cache for a later render
// to use (the Python cache wrote in place).
type voices struct {
	speaker      Voicer
	service      speech.Service
	brief        Brief
	instructions string
	directory    string
	limit        chan struct{}

	mu       sync.Mutex
	inflight map[string]*pending
}

type pending struct {
	done chan struct{}
	path string
	err  error
}

func newVoices(speaker Voicer, service speech.Service, brief Brief, instructions, directory string, concurrency int) (*voices, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("the podcast's working directory: %w", err)
	}
	return &voices{
		speaker: speaker, service: service, brief: brief, instructions: instructions,
		directory: directory, limit: make(chan struct{}, concurrency), inflight: map[string]*pending{},
	}, nil
}

// line is one turn as the speech service is asked for it.
type line struct {
	key    string
	speech speech.Line
}

// lineFor resolves a turn's speaker and voice. A turn naming a slot the
// brief does not have, or a voice the service does not speak, is an error
// here rather than a line in some other voice.
func (v *voices) lineFor(turn Turn) (line, error) {
	speaker, found := v.brief.speaker(turn.Speaker)
	if !found {
		return line{}, fmt.Errorf("the transcript names speaker slot %d, which the brief does not have", turn.Speaker)
	}
	voice, err := v.service.Voice(speaker.VoiceID)
	if err != nil {
		return line{}, err
	}
	style := v.instructions
	if speaker.Delivery != nil && *speaker.Delivery != "" {
		style = *speaker.Delivery
	}
	if !v.service.TakesInstructions() {
		// A model that takes no style is not sent one, so the style must
		// not split the cache either.
		style = ""
	}
	key, err := json.Marshal([]any{v.service.Model, voice, v.brief.Language, style, turn.Text})
	if err != nil {
		return line{}, err
	}
	sum := sha256.Sum256(key)
	return line{key: hex.EncodeToString(sum[:]), speech: speech.Line{Text: turn.Text, Voice: voice, Instructions: style}}, nil
}

// path is the file a turn is voiced into, voicing it if it is not cached.
func (v *voices) path(ctx context.Context, turn Turn) (string, error) {
	wanted, err := v.lineFor(turn)
	if err != nil {
		return "", err
	}
	target := filepath.Join(v.directory, wanted.key+".mp3")
	if _, err := os.Stat(target); err == nil {
		return target, nil
	}

	v.mu.Lock()
	waiting, already := v.inflight[wanted.key]
	if !already {
		waiting = &pending{done: make(chan struct{})}
		v.inflight[wanted.key] = waiting
	}
	v.mu.Unlock()
	if already {
		select {
		case <-waiting.done:
			return waiting.path, waiting.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	waiting.path, waiting.err = v.voice(ctx, wanted, target)
	v.mu.Lock()
	delete(v.inflight, wanted.key)
	v.mu.Unlock()
	close(waiting.done)
	return waiting.path, waiting.err
}

func (v *voices) voice(ctx context.Context, wanted line, target string) (string, error) {
	select {
	case v.limit <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-v.limit }()
	// Voiced by another caller while this one waited for a slot.
	if _, err := os.Stat(target); err == nil {
		return target, nil
	}
	audio, err := v.speaker.Speak(ctx, wanted.speech)
	if err != nil {
		return "", err
	}
	temporary, err := os.CreateTemp(v.directory, ".line-*")
	if err != nil {
		return "", err
	}
	_, writeErr := temporary.Write(audio)
	closeErr := temporary.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		os.Remove(temporary.Name())
		return "", err
	}
	if err := os.Rename(temporary.Name(), target); err != nil {
		os.Remove(temporary.Name())
		return "", err
	}
	return target, nil
}

// all voices every turn, in order, and returns the files in the same order.
// The first failure stops the rest, and so does check, which is asked
// before each line: it is how a cancel stops a render between lines.
func (v *voices) all(ctx context.Context, turns []Turn, check func(context.Context) error) ([]string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	paths := make([]string, len(turns))
	errs := make(chan error, len(turns))
	var wait sync.WaitGroup
	for index, turn := range turns {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := check(ctx); err != nil {
				errs <- err
				cancel()
				return
			}
			path, err := v.path(ctx, turn)
			if err != nil {
				errs <- fmt.Errorf("line %d: %w", index+1, err)
				cancel()
				return
			}
			paths[index] = path
		}()
	}
	wait.Wait()
	close(errs)
	if err, failed := <-errs; failed {
		return nil, err
	}
	return paths, nil
}
