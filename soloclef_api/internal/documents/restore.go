package documents

import (
	"context"
	"strconv"
	"time"
)

// app/utils/document_versioning.py's rules, less the one the restore turns
// off. DEBOUNCE_MINUTES is not here: the helper overwrites a newest version
// younger than half an hour instead of adding a row, and the restore calls it
// with `debounce=False`.
const (
	// RETENTION_DAYS, as the timedelta it is subtracted as: ninety days of
	// exactly twenty-four hours.
	versionRetention = 90 * 24 * time.Hour
	// MAX_VERSIONS_PER_DOCUMENT.
	mostVersions = 20
)

// RestoreWork is what restore_document_version holds when it stops refusing
// and starts writing: the document and the version as it loaded them, and
// the instant create_version_snapshot calls `now`.
type RestoreWork struct {
	Document Restorable
	Version  Version
	// Now is the application's clock, not the database's: the Python helper
	// calls datetime.now(UTC) and writes that into `created_at`.
	Now time.Time
}

// snapshot is the document's state as a version row stores it.
type snapshot struct {
	sourceMarkdown *string
	contentHash    string
	title          string
	takenAt        time.Time
}

// versionRows is the statements a restore issues, in the order it issues
// them. It is an interface so the same steps run over a transaction
// (store.go) and over a table of rows in the tests,
// which is how the order of these statements is held to the Python helper's.
type versionRows interface {
	HighestVersionNumber(ctx context.Context, documentID int64) (int64, error)
	InsertVersion(ctx context.Context, documentID, number int64, state snapshot) error
	DeleteVersionsOlderThan(ctx context.Context, documentID int64, cutoff time.Time) error
	CountVersions(ctx context.Context, documentID int64) (int64, error)
	OldestVersionIDs(ctx context.Context, documentID, excess int64) ([]int64, error)
	DeleteVersions(ctx context.Context, documentID int64, versionIDs []int64) error
	// UpdateDocument writes the restored text and title onto the owner's
	// document.
	UpdateDocument(ctx context.Context, documentID int64, userID string, sourceMarkdown *string, title string) error
}

// applyRestore is `create_version_snapshot(session, document, debounce=False)`
// followed by the three assignments restore_document_version makes before it
// commits.
//
// # The snapshot is always a new version
//
// The document's current state is kept before it is overwritten: a version
// is added, numbered one past the highest, and two clean-ups run -- versions
// older than ninety days go, and then the oldest go until twenty are left.
//
// The Python helper has a second way of snapshotting, for its other callers:
// when the newest version is younger than half an hour it overwrites that
// row and adds none. The folder indexer wants that, because its snapshots
// are repeats. The restore must not have it, because its snapshot is the
// only record of the text it is about to replace, and the overwrite spent an
// existing version to make it. Until 2026-11-11 the restore did debounce,
// and lost text three ways: restoring the newest version soon after it was
// written restored nothing and destroyed it; editing and then restoring an
// older version destroyed the text from before the edit; restoring one
// version twice destroyed what the document had said before either. All
// three are in tests/integration/routes/test_document_version_restore.py.
//
// So there is no half hour here at all, and no statement that reads the
// newest version: the Python route does not issue one either.
//
// # What it costs, which is the retention rules' to take
//
// Every restore now runs the two clean-ups, where a restore inside the half
// hour used to skip them. So a restore can remove a version it would once
// have left: one past ninety days, or the oldest of twenty. That can be the
// version being restored. The document still gets its text, because
// `work.Version` is a value the caller read before this function ran -- the
// route reads the markdown and the title into two locals before it
// snapshots -- and nothing below writes to it.
func applyRestore(ctx context.Context, rows versionRows, work RestoreWork) error {
	document := work.Document
	now := microseconds(work.Now)
	current := snapshot{
		sourceMarkdown: document.SourceMarkdown,
		contentHash:    document.ContentHash,
		title:          document.Title,
		takenAt:        now,
	}
	// `restoring_markdown = version.source_markdown` and
	// `restoring_title = version.title`, read before the snapshot so that no
	// statement below -- the two clean-ups can delete the row -- can change
	// what is restored.
	restoredMarkdown, restoredTitle := work.Version.SourceMarkdown, work.Version.Title

	highest, err := rows.HighestVersionNumber(ctx, document.ID)
	if err != nil {
		return err
	}
	if err := rows.InsertVersion(ctx, document.ID, highest+1, current); err != nil {
		return err
	}
	if err := rows.DeleteVersionsOlderThan(ctx, document.ID, now.Add(-versionRetention)); err != nil {
		return err
	}
	count, err := rows.CountVersions(ctx, document.ID)
	if err != nil {
		return err
	}
	if count > mostVersions {
		doomed, err := rows.OldestVersionIDs(ctx, document.ID, count-mostVersions)
		if err != nil {
			return err
		}
		// `if oldest_ids:` -- the read can come back empty when another
		// request has removed them in between.
		if len(doomed) > 0 {
			if err := rows.DeleteVersions(ctx, document.ID, doomed); err != nil {
				return err
			}
		}
	}

	// `document.title = restoring_title or document.title`: a version with no
	// title, or an empty one, leaves the document's alone. The markdown has
	// no such fallback -- a version with none clears the document's.
	title := document.Title
	if restoredTitle != nil && *restoredTitle != "" {
		title = *restoredTitle
	}
	return rows.UpdateDocument(ctx, document.ID, document.UserID, restoredMarkdown, title)
}

// restoredMessage is `f"Restored version {version_number}"`.
func restoredMessage(versionNumber int64) string {
	return "Restored version " + strconv.FormatInt(versionNumber, 10)
}

// microseconds is an instant as Python's datetime holds one and as a
// timestamptz column stores one: whole microseconds, the rest dropped rather
// than rounded. The snapshot's age and the ninety-day cutoff are both worked
// out from the clock's reading, so they are worked out from the same reading
// the column will hold.
func microseconds(moment time.Time) time.Time {
	return moment.Truncate(time.Microsecond)
}
