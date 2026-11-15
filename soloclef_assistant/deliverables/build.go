package deliverables

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	filestore "git.danvics.com/danvics/soloclef/soloclef_filestore"
	"git.danvics.com/danvics/soloclef/soloclef_studio/posterrender"
	"git.danvics.com/danvics/soloclef/soloclef_studio/sandbox"
)

// The choices below are the specialist's own, not a deployment's: they are
// how it is meant to run, they are named, and each says why it is what it
// is. They are here, once, so that an artifact asked for in chat and one
// made by an automation's run are made within the same limits.
const (
	// Making an artifact is: load instructions, save, perhaps fix and save
	// again, answer: three steps for a slide deck, which is saved from its
	// content (slidedeck.go). A document built by code is more:
	// instructions, a few commands, a check, a repair and a second check, a
	// save, about seventeen steps, and a long report the layout rules send
	// back runs past thirty on a live model (13 Nov 2026, measured on decks
	// when they were built that way: two of three stopped at thirty), so
	// sixty leaves room for two repairs and a model that writes one section
	// per command. Twenty minutes covers a cold sandbox, a long render, and
	// a server busy with other builds.
	specialistSteps   = 60
	specialistTimeout = 20 * time.Minute

	// UnattendedSandboxWait is the longest a Studio output or an
	// automation's step waits for a place in its account's sandbox limit
	// (sandboxwait.go). Half of specialistTimeout, because the wait is
	// spent inside it and the other half must still build the artifact: a
	// report is about seventeen steps, a deck's preview one conversion.
	// And long enough for what holds a place to let it go: another output
	// releases its sandbox the moment it ends (decks and reports finish in
	// a few minutes), and a chat's expires at its idle time to live, ten
	// minutes on the live deployment.
	UnattendedSandboxWait = 10 * time.Minute

	// One poster's PNG: resvg draws a full one in about a quarter of a
	// second (posterrender's TestPNGs); thirty leaves room for a server
	// busy with other work, and still stops a stuck process.
	posterTimeout = 30 * time.Second

	// A podcast is proposed in the shape the podcast routes propose when a
	// request names none: two voices, twenty to thirty seconds. The person
	// sees all three on the card and changes them there before anything
	// is drafted.
	podcastSpeakers   = 2
	podcastMinSeconds = 20
	podcastMaxSeconds = 30
)

// Settings is what a deployment says about the specialist. There is no
// default for anything that names a server or a model.
type Settings struct {
	// ModelURL and ModelKey reach the model proxy, the only way a model is
	// called.
	ModelURL, ModelKey string
	// Resvg is the resvg program an infographic's PNG is drawn with, and
	// FigureFonts figures-go's font directory (the image's copy of the
	// module's fonts/), which the figures on a poster are set in. Both
	// required: every deployment draws infographics, and nothing is drawn
	// in fonts other than the ones the layout measured.
	Resvg       string
	FigureFonts string
	// SandboxEnabled says reports, slides, spreadsheets and interactive
	// pages are built, in the sandbox Sandbox describes. Off, a person who
	// asks for one is told it is not available here.
	SandboxEnabled bool
	Sandbox        sandbox.Settings
}

// Parts is what the process already has.
type Parts struct {
	Pool *pgxpool.Pool
	// Store is where the specialist's own conversation is kept while it
	// works: the store the turn's agent saves to.
	Store checkpoint.Store
	// Files is where a saved artifact's file is written.
	Files filestore.Writer
	// HTTP must have no whole-request timeout; each call is bounded here.
	HTTP *http.Client
	// Voices is the podcast voice catalogue a podcast's voices are
	// proposed from.
	Voices VoiceCatalogue
	// PublishVideo puts a video job's message on the tier's queue
	// (VideoMessage is the message).
	PublishVideo func(ctx context.Context, jobID int64, taskID string) error
	// Index queues a saved artifact's document for indexing.
	Index  func(ctx context.Context, documentID int64, userID string) error
	Logger *slog.Logger
}

// NewSpecialist builds the specialist a tier gives its turns (PerTurn), with its
// model left for each turn to set. Each optional part is either the
// configured one or the type that says, in a sentence to the person, that
// this deployment has none; nothing stands in for another.
//
// forget removes a thread's sandbox, for the tier that deletes threads;
// with no sandbox there is nothing to remove.
func NewSpecialist(settings Settings, parts Parts) (specialist Specialist, forget func(ctx context.Context, threadID int64) error, err error) {
	switch {
	case parts.Pool == nil || parts.Store == nil || parts.Files == nil || parts.HTTP == nil || parts.Voices == nil || parts.Logger == nil:
		return Specialist{}, nil, errors.New("deliverables: the pool, the checkpoint store, the file store, the HTTP client, the voices and the logger are all required")
	case parts.PublishVideo == nil || parts.Index == nil:
		return Specialist{}, nil, errors.New("deliverables: how a video job is published and how a document is indexed are both required")
	}
	// The deck's faces are written into a directory of the rasteriser's
	// own, once per process: resvg reads fonts from directories only.
	work, err := os.MkdirTemp("", "soloclef-posters-")
	if err != nil {
		return Specialist{}, nil, fmt.Errorf("deliverables: a directory for the poster fonts: %w", err)
	}
	posters, err := posterrender.NewRasteriser(settings.Resvg, settings.FigureFonts, work, posterTimeout)
	if err != nil {
		return Specialist{}, nil, err
	}
	var workshop Workshop = NoSandbox{}
	var verified *Verifications
	forget = func(context.Context, int64) error { return nil }
	if settings.SandboxEnabled {
		sandboxes, err := sandbox.New(settings.Sandbox, parts.HTTP)
		if err != nil {
			return Specialist{}, nil, err
		}
		workshop = Sandboxes{Client: sandboxes}
		verified = NewVerifications()
		forget = sandboxes.Terminate
	}
	return Specialist{
		// Model is set per turn, to the turn's (PerTurn).
		Store:    parts.Store,
		Limits:   Limits{MaxSteps: specialistSteps, Timeout: specialistTimeout},
		Posters:  posters,
		Videos:   Videos{Rows: VideoStore{Pool: parts.Pool}, Publish: parts.PublishVideo, Logger: parts.Logger},
		Workshop: workshop, Verified: verified,
		// The podcast's row is written by the specialist's own store, the
		// one every tier that runs it uses.
		Podcasts: Podcasts{
			Rows: PodcastStore{Pool: parts.Pool}, Voices: parts.Voices, Logger: parts.Logger,
			Brief: PodcastBrief{Speakers: podcastSpeakers, MinSeconds: podcastMinSeconds, MaxSeconds: podcastMaxSeconds},
		},
		Saver: Saver{
			Rows: Store{Pool: parts.Pool}, Files: parts.Files, Logger: parts.Logger,
			NewID: newID, Index: parts.Index,
		},
	}, forget, nil
}

// newID is sixteen random bytes as hex: a stored file's own name, which
// nobody has to guess.
func newID() string {
	raw := make([]byte, 16)
	// crypto/rand.Read does not fail on any platform Go supports.
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}
