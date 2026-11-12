package activitylogs

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// Store reads and writes the account's log. Every method takes the owner, and
// every statement is one round trip that names it, so none needs a
// transaction: there is no check to keep consistent with the write.
type Store struct {
	Pool *pgxpool.Pool
}

// Entry is one entry of the caller's log, or nil when it is not the caller's
// or is not there. The two are deliberately the same answer.
func (s Store) Entry(ctx context.Context, ownerID string, logID int64) (*Entry, error) {
	row, err := New(s.Pool).LogOfOwner(ctx, LogOfOwnerParams{ID: logID, UserID: ownerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Entry{
		CreatedAt: row.CreatedAt, ID: row.ID, Level: row.Level, Status: row.Status,
		Message: row.Message, Source: row.Source, Metadata: row.LogMetadata,
	}, nil
}

// Insert writes a new entry for the owner and returns the row as stored.
func (s Store) Insert(ctx context.Context, ownerID string, requested Requested, createdAt time.Time) (Entry, error) {
	metadata, err := storedMetadata(requested.Metadata)
	if err != nil {
		return Entry{}, err
	}
	row, err := New(s.Pool).CreateLog(ctx, CreateLogParams{
		UserID:      ownerID,
		Level:       Loglevel(requested.Level),
		Status:      Logstatus(requested.Status),
		Message:     requested.Message,
		Source:      requested.Source,
		LogMetadata: metadata,
		CreatedAt:   createdAt,
	})
	if err != nil {
		return Entry{}, err
	}
	return Entry{
		CreatedAt: row.CreatedAt, ID: row.ID, Level: row.Level, Status: row.Status,
		Message: row.Message, Source: row.Source, Metadata: row.LogMetadata,
	}, nil
}

// Update writes the fields the body mentioned to the owner's entry and
// returns it, or nil when the entry is not the owner's or is not there. A
// body that mentioned nothing writes nothing and reads the entry instead.
func (s Store) Update(ctx context.Context, ownerID string, logID int64, changes Changes) (*Entry, error) {
	if !changes.Mentions() {
		return s.Entry(ctx, ownerID, logID)
	}
	metadata, err := storedMetadata(changes.Metadata.Value)
	if err != nil {
		return nil, err
	}
	row, err := New(s.Pool).UpdateLog(ctx, UpdateLogParams{
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
		UserID:         ownerID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Entry{
		CreatedAt: row.CreatedAt, ID: row.ID, Level: row.Level, Status: row.Status,
		Message: row.Message, Source: row.Source, Metadata: row.LogMetadata,
	}, nil
}

// Delete removes the owner's entry and reports whether there was one.
func (s Store) Delete(ctx context.Context, ownerID string, logID int64) (bool, error) {
	removed, err := New(s.Pool).DeleteLog(ctx, DeleteLogParams{ID: logID, UserID: ownerID})
	return removed > 0, err
}

// storedMetadata is the column's value for a request's metadata: the object
// as JSON, or SQL NULL for none. There is one way to say "no metadata".
func storedMetadata(metadata *pythonjson.Object) ([]byte, error) {
	if metadata == nil {
		return nil, nil
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
