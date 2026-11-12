package deliverables

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/accountvision"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
	filestore "git.danvics.com/danvics/soloclef/soloclef_filestore"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/vision"
	"git.danvics.com/danvics/soloclef/soloclef_studio/sandbox"
)

// The choices below are the specialist's own, not a deployment's: they are
// how it is meant to run, they are named, and each says why it is what it
// is. They are here, once, so that an artifact asked for in chat and one
// made by an automation's run are made within the same limits.
const (
	// Making an artifact is: load instructions, save, perhaps fix and save
	// again, answer. A built document is more: instructions, a few
	// commands, a check, a repair and a second check, a save, about
	// seventeen steps; thirty is that with room for one more repair.
	// Twelve minutes covers a cold sandbox, a long render and an
	// infographic drawn and read twice.
	specialistSteps   = 30
	specialistTimeout = 12 * time.Minute

	// One image: models take up to two minutes; a picture over 32 MiB is
	// not one a card shows. Reading an infographic back is one vision
	// call.
	imageTimeout     = 150 * time.Second
	imageMaxBytes    = 32 << 20
	imageReadTimeout = 60 * time.Second

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
	// ImageModel is the proxy's name for the model that draws images and
	// infographics. Empty means this deployment draws none, and a person
	// who asks for one is told so; no other model is tried.
	ImageModel string
	// VisionModel is the proxy's name for the model that reads a drawn
	// infographic back to check its text, for an account that chose no
	// vision model of its own: an image model's pixels are the one thing
	// here no rule can read. Empty means such an account's infographics
	// are saved marked unchecked. A built document's pages are never
	// looked at by a model: they are measured (layout_rules.go).
	VisionModel string
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
	// Own opens a model of an account's own connection as its vision
	// model (ownmodels.Connections, the one the turn's model is opened
	// with), for an account that chose one.
	Own ownmodels.VisionOpener
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
	case parts.Pool == nil || parts.Store == nil || parts.Files == nil || parts.HTTP == nil || parts.Voices == nil || parts.Logger == nil || parts.Own == nil:
		return Specialist{}, nil, errors.New("deliverables: the pool, the checkpoint store, the file store, the HTTP client, the voices, the logger and the own model connections are all required")
	case parts.PublishVideo == nil || parts.Index == nil:
		return Specialist{}, nil, errors.New("deliverables: how a video job is published and how a document is indexed are both required")
	}
	var images ImageModel = NoImageModel{}
	if settings.ImageModel != "" {
		images = ProxyImages{BaseURL: settings.ModelURL, Key: settings.ModelKey, Model: settings.ImageModel, HTTP: parts.HTTP, Timeout: imageTimeout, MaxBytes: imageMaxBytes}
	}
	// The model that reads an account's infographics back: its own vision
	// choice, else VisionModel (accountvision).
	sight, err := accountvision.ForDeployment(parts.Pool,
		vision.Client{BaseURL: settings.ModelURL, Key: settings.ModelKey, HTTP: parts.HTTP, Timeout: imageReadTimeout},
		parts.Own, settings.VisionModel)
	if err != nil {
		return Specialist{}, nil, err
	}
	reader := VisionImageReader{Sight: sight, Timeout: imageReadTimeout}
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
		Store:  parts.Store,
		Limits: Limits{MaxSteps: specialistSteps, Timeout: specialistTimeout},
		Images: images, Reader: reader,
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
