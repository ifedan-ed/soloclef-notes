package workspacelogs

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// Store reads logs. Its read method takes the pool and the write helpers take a
// transaction, which is the seam: a write's statements all run on the pgx.Tx
// that internal/database.Write opened.
type Store struct {
	Pool *pgxpool.Pool
}

// Entry is read_log's query: the row, resolved only inside the workspaces the
// caller is a member of, as _authorised_log resolves it. A log the caller
// cannot reach is reported absent rather than refused, which is what stops the
// route being an existence oracle.
func (s Store) Entry(ctx context.Context, callerID string, logID int64) (*Entry, error) {
	return readEntry(New(s.Pool), ctx, callerID, logID)
}

// EntryIn is the same question asked inside a write's transaction.
func EntryIn(ctx context.Context, tx pgx.Tx, callerID string, logID int64) (*Entry, error) {
	return readEntry(New(tx), ctx, callerID, logID)
}

func readEntry(queries *Queries, ctx context.Context, callerID string, logID int64) (*Entry, error) {
	row, err := queries.LogForMember(ctx, LogForMemberParams{ID: logID, UserID: callerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Entry{
		CreatedAt:   row.CreatedAt,
		ID:          row.ID,
		Level:       row.Level,
		Status:      row.Status,
		Message:     row.Message,
		Source:      row.Source,
		Metadata:    row.LogMetadata,
		WorkspaceID: row.WorkspaceID,
	}, nil
}

// Insert writes the new log and returns the row as stored, which is what
// LogRead renders — the Python route refreshes the instance for the same
// reason.
func Insert(ctx context.Context, tx pgx.Tx, requested Requested, createdAt time.Time) (Entry, error) {
	metadata, err := storedMetadata(requested.Metadata)
	if err != nil {
		return Entry{}, err
	}
	row, err := New(tx).CreateLog(ctx, CreateLogParams{
		Level:       Loglevel(requested.Level),
		Status:      Logstatus(requested.Status),
		Message:     requested.Message,
		Source:      requested.Source,
		LogMetadata: metadata,
		WorkspaceID: requested.WorkspaceID,
		CreatedAt:   createdAt,
	})
	if err != nil {
		return Entry{}, err
	}
	return Entry{
		CreatedAt:   row.CreatedAt,
		ID:          row.ID,
		Level:       row.Level,
		Status:      row.Status,
		Message:     row.Message,
		Source:      row.Source,
		Metadata:    row.LogMetadata,
		WorkspaceID: row.WorkspaceID,
	}, nil
}

// Update writes the fields the body mentioned.
func Update(ctx context.Context, tx pgx.Tx, logID int64, changes Changes) (Entry, error) {
	metadata, err := storedMetadata(changes.Metadata.Value)
	if err != nil {
		return Entry{}, err
	}
	row, err := New(tx).UpdateLog(ctx, UpdateLogParams{
		SetLevel:       changes.Level.Present,
		Level:          nullLevel(changes.Level.Value),
		SetStatus:      changes.Status.Present,
		Status:         nullStatus(changes.Status.Value),
		SetMessage:     changes.Message.Present,
		Message:        changes.Message.Value,
		SetSource:      changes.Source.Present,
		Source:         changes.Source.Value,
		SetLogMetadata: changes.Metadata.Present,
		LogMetadata:    metadata,
		ID:             logID,
	})
	if err != nil {
		return Entry{}, err
	}
	return Entry{
		CreatedAt:   row.CreatedAt,
		ID:          row.ID,
		Level:       row.Level,
		Status:      row.Status,
		Message:     row.Message,
		Source:      row.Source,
		Metadata:    row.LogMetadata,
		WorkspaceID: row.WorkspaceID,
	}, nil
}

// Delete removes the log.
func Delete(ctx context.Context, tx pgx.Tx, logID int64) error {
	return New(tx).DeleteLog(ctx, logID)
}

// storedMetadata is the text SQLAlchemy's JSON type would have written for the
// same value, so a row this tier writes is the row that tier would have written
// (internal/pythonjson.MarshalColumn).
//
// log_metadata=None is **not** a SQL NULL. SQLAlchemy's JSON type leaves
// none_as_null at its default of False, so a Python None is serialised to the
// JSON scalar `null` and the column holds four bytes rather than nothing. The
// difference is invisible in the response — both tiers read the column back
// through json.loads and render None either way — and it was found by diffing
// the rows after the same POST on each tier.
//
// Reading is the other way round and stays tolerant: a column that *is* SQL
// NULL, as rows written by other means can be, still renders as None.
func storedMetadata(metadata *pythonjson.Object) ([]byte, error) {
	if metadata == nil {
		return []byte("null"), nil
	}
	return pythonjson.MarshalColumn(metadata)
}

func nullLevel(value *string) NullLoglevel {
	if value == nil {
		return NullLoglevel{}
	}
	return NullLoglevel{Loglevel: Loglevel(*value), Valid: true}
}

func nullStatus(value *string) NullLogstatus {
	if value == nil {
		return NullLogstatus{}
	}
	return NullLogstatus{Logstatus: Logstatus(*value), Valid: true}
}
