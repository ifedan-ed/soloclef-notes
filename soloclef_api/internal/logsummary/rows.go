package logsummary

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Reader reads the rows the summary counts.
type Reader struct {
	Pool *pgxpool.Pool
}

// InWindow is the Python route's own query: every log of one workspace since
// the cutoff, newest first and with no limit.
//
// The enum columns are cast to text because this tier has no reason to know
// Postgres' enum oids, and log_metadata is read as text so its key order
// survives into the response.
func (r Reader) InWindow(ctx context.Context, workspaceID int64, since time.Time) ([]Row, error) {
	rows, err := r.Pool.Query(ctx, `
		SELECT id, status::text, level::text, message, source, created_at, log_metadata::text
		FROM logs
		WHERE workspace_id = $1 AND created_at >= $2
		ORDER BY created_at DESC`, workspaceID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	collected := []Row{}
	for rows.Next() {
		var (
			row      Row
			metadata *string
		)
		if err := rows.Scan(
			&row.ID, &row.Status, &row.Level, &row.Message, &row.Source, &row.CreatedAt, &metadata,
		); err != nil {
			return nil, err
		}
		if metadata != nil {
			row.Metadata = []byte(*metadata)
		}
		collected = append(collected, row)
	}
	return collected, rows.Err()
}
