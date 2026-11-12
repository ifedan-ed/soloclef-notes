package podcasts

import (
	"context"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// Stored is one `podcast_runs` row as a route holds it, with the two jsonb
// columns decoded.
type Stored struct {
	ID          int64
	Title       string
	Status      string
	SpecVersion int64
	// Spec and Transcript are the decoded columns: nil for a SQL NULL and
	// for the jsonb value null alike.
	Spec            any
	Transcript      any
	DurationSeconds *int64
	Error           *string
	// ArtifactID is the delivered episode, which owns the audio. A podcast
	// "has a finished episode" exactly when this is set.
	ArtifactID *int64
	ThreadID   *int64
	CreatedAt  time.Time
}

// newStored decodes a row's two jsonb columns.
func newStored(row PodcastOfOwnerRow) (*Stored, error) {
	spec, err := decodeColumn(row.Spec)
	if err != nil {
		return nil, err
	}
	transcript, err := decodeColumn(row.PodcastTranscript)
	if err != nil {
		return nil, err
	}
	return &Stored{
		ID:              row.ID,
		Title:           row.Title,
		Status:          row.Status,
		SpecVersion:     row.SpecVersion,
		Spec:            spec,
		Transcript:      transcript,
		DurationSeconds: row.DurationSeconds,
		Error:           row.Error,
		ArtifactID:      row.ArtifactID,
		ThreadID:        row.ThreadID,
		CreatedAt:       row.CreatedAt,
	}, nil
}

func decodeColumn(raw []byte) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	// pythonjson keeps an object's keys in order and an integer at any
	// width, so nothing about the stored value is lost on the way in.
	return pythonjson.Decode(raw)
}

// NewPodcast is a podcast to be created at the brief gate.
type NewPodcast struct {
	Title         string
	SourceContent string
	// Spec is the proposed brief, as JSON.
	Spec     []byte
	ThreadID *int64
}

// Reading is the statements a read is made of. Each takes the owner's id
// beside the podcast's, and answers nothing for a podcast that is not that
// owner's: the routes pass the signed-in caller and never anything else.
type Reading interface {
	// Podcast is the caller's row, or nil when they have none by that id.
	Podcast(ctx context.Context, userID string, podcastID int64) (*Stored, error)
}

// Writing is Reading and the statements a write is made of.
type Writing interface {
	Reading
	// Move takes the podcast from one status to another and reports whether
	// it did: false means the row is gone or is no longer in `current`.
	Move(ctx context.Context, userID string, podcastID int64, current, target string) (bool, error)
	// Delete removes the row and reports whether there was one to remove.
	Delete(ctx context.Context, userID string, podcastID int64) (bool, error)
	// LatestBrief is the decoded brief of the caller's most recent podcast
	// that has one, or nil.
	LatestBrief(ctx context.Context, userID string) (any, error)
	// OwnsThread reports whether the thread is one of the caller's.
	OwnsThread(ctx context.Context, userID string, threadID int64) (bool, error)
	// Create inserts the podcast for its owner and answers the row.
	Create(ctx context.Context, userID string, podcast NewPodcast) (*Stored, error)
	// ReplaceBrief writes an edited brief and reports whether it did: false
	// means the podcast is gone, has left the brief gate, or is no longer at
	// the version the edit was made against.
	ReplaceBrief(ctx context.Context, userID string, podcastID, expectedVersion int64, spec []byte) (bool, error)
}

// Database is where a route's statements run. It is an interface, and the
// work is handed to it as a function, so the same route code runs over the
// pool, inside one transaction, and against a stand-in in tests.
type Database interface {
	// Read runs work with no transaction of its own.
	Read(ctx context.Context, work func(context.Context, Reading) error) error
	// Write runs work in one transaction: committed when work returns nil,
	// rolled back when it returns anything else. A refusal is returned as an
	// error for exactly that reason.
	Write(ctx context.Context, work func(context.Context, Writing) error) error
}
