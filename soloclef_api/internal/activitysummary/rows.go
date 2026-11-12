package activitysummary

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Reader reads the rows the summary counts.
type Reader struct {
	Pool *pgxpool.Pool
}

// InWindow runs the route's query (queries.sql) and hands the rows over in the
// order it returned them, which is the order the summary's keys depend on.
func (r Reader) InWindow(ctx context.Context, ownerID string, since time.Time) ([]Row, error) {
	found, err := New(r.Pool).LogsInWindow(ctx, LogsInWindowParams{
		UserID: ownerID,
		Since:  since,
	})
	if err != nil {
		return nil, err
	}
	rows := make([]Row, 0, len(found))
	for _, row := range found {
		rows = append(rows, Row{
			ID:        row.ID,
			Status:    row.Status,
			Level:     row.Level,
			Message:   row.Message,
			Source:    row.Source,
			CreatedAt: row.CreatedAt,
			Metadata:  row.LogMetadata,
		})
	}
	return rows, nil
}
