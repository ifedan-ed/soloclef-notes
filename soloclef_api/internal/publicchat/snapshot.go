package publicchat

import (
	"context"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// Snapshot is the three columns of public_chat_snapshots these routes read.
//
// It is deliberately not the whole row. These routes answer a caller with no
// credential, so the set of columns they can see is a decision rather than a
// convenience: the account that made the link and the content
// hash are not here, and so cannot be sent by a later edit to a renderer.
type Snapshot struct {
	// OutputID is notebook_output_id: set when the link shares one Studio
	// output rather than a chat. A check constraint makes it the opposite of
	// thread_id, which is why only one of the two is read.
	OutputID *int64
	// ExpiresAt is nil for a link that never expires.
	ExpiresAt *time.Time
	// Data is snapshot_data as the column's text.
	Data []byte
}

// VideoArtifact is what get_snapshot_video_artifact reads off the artifact
// and its document.
type VideoArtifact struct {
	ID int64
	// Title is nil when the artifact has no document row, which is
	// `artifact.document` being None.
	Title *string
	// Metadata is artifacts.metadata as the column's text, nil for NULL.
	Metadata []byte
}

// StoredFile is one row of artifact_files, less everything that says where
// the bytes are.
type StoredFile struct {
	ID       int64
	Role     string
	Filename string
	MimeType string
	Size     int64
}

// Rows is the five statements the three routes issue. The routes take it as
// an interface so the recorded answers can be replayed with the stub's world
// in place of a database, and the order of the calls compared with the order
// of the Python statements.
type Rows interface {
	// SnapshotByToken is _snapshot_row: nil when the token names nothing.
	SnapshotByToken(ctx context.Context, token string) (*Snapshot, error)
	// VideoArtifact is get_snapshot_video_artifact's statement: nil when
	// there is no such artifact or it is not a video.
	VideoArtifact(ctx context.Context, artifactID int64) (*VideoArtifact, error)
	// ArtifactGeneration is shared_output_files' first statement; found is
	// false when the artifact is gone.
	ArtifactGeneration(ctx context.Context, artifactID int64) (generation int64, found bool, err error)
	// ArtifactFiles is its second: every file of the artifact, in no order.
	ArtifactFiles(ctx context.Context, artifactID int64) ([]StoredFile, error)
	// ArtifactFormats is the legacy upcast's statement: id to format, for the
	// ids that exist.
	ArtifactFormats(ctx context.Context, artifactIDs []int64) (map[int64]string, error)
}

// expired is app/public_sharing/expiry.py:is_expired. The comparison is `<=`:
// a link is already expired at the instant it expires.
func expired(expiresAt *time.Time, now time.Time) bool {
	return expiresAt != nil && !expiresAt.After(now)
}

// liveSnapshot is get_snapshot_by_token: the snapshot, or nil for a token
// that names nothing *and* for one whose link has expired. The video and
// podcast routes use it, which is why they cannot tell the two apart; the
// page reads the row itself and can.
func liveSnapshot(ctx context.Context, rows Rows, token string, now time.Time) (*Snapshot, error) {
	found, err := rows.SnapshotByToken(ctx, token)
	if err != nil || found == nil {
		return nil, err
	}
	if expired(found.ExpiresAt, now) {
		return nil, nil
	}
	return found, nil
}

// snapshotData is `snapshot.snapshot_data` as a route uses it: every route's
// next step is `.get(...)` on it, so anything but an object is an
// AttributeError. The column is NOT NULL, but the JSON scalar null satisfies
// that and is None to Python.
func snapshotData(found *Snapshot) (*pythonjson.Object, error) {
	value, err := decodeColumn(found.Data)
	if err != nil {
		return nil, err
	}
	data, isObject := value.(*pythonjson.Object)
	if !isObject {
		return nil, raised("snapshot_data is not an object, and the route calls .get on it")
	}
	return data, nil
}
