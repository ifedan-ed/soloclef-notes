package notebookname

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrGone is a notebook that no longer exists.
var ErrGone = errors.New("notebookname: the notebook is gone")

// Notebook is the notebook as the naming reads it.
type Notebook struct {
	ID          int64
	UserID      string
	Title       string
	TitleSource string
	AutoTitle   []byte
}

// Sources is what the notebook's ready sources give a name to draw on.
type Sources struct {
	// Ready is how many document sources are ready.
	Ready int
	// First is the oldest few, as excerpts.
	First []excerpt
}

// Naming is what the naming is written as.
type Naming struct {
	Title string
	// Icon is "" to leave the icon as it is.
	Icon      string
	AutoTitle []byte
	// ExpectedRuns is the run count the decision was made on. The write
	// lands only if the notebook is still self-named at that count, so a
	// rename the person makes while the model is thinking always wins, and
	// two jobs that raced name it once.
	ExpectedRuns int
	// OldTitle is the name the notebook had, which its outputs' titles may
	// still carry.
	OldTitle string
}

// Rows is the naming's database.
type Rows interface {
	Notebook(ctx context.Context, notebookID int64) (Notebook, error)
	ReadySources(ctx context.Context, notebook Notebook) (Sources, error)
	// Rename reports whether the name was written.
	Rename(ctx context.Context, notebook Notebook, naming Naming) (bool, error)
}

// Store is Rows over Postgres.
type Store struct {
	Pool *pgxpool.Pool
}

func (s Store) Notebook(ctx context.Context, notebookID int64) (Notebook, error) {
	notebook := Notebook{ID: notebookID}
	err := s.Pool.QueryRow(ctx, `
		SELECT user_id::text, title, title_source, auto_title FROM notebooks WHERE id = $1`, notebookID).
		Scan(&notebook.UserID, &notebook.Title, &notebook.TitleSource, &notebook.AutoTitle)
	if errors.Is(err, pgx.ErrNoRows) {
		return Notebook{}, ErrGone
	}
	return notebook, err
}

// ReadySources reads the excerpts in one statement. Only the start of each
// document crosses the wire: the material uses its first 1,500 characters,
// and a source can be a whole book.
func (s Store) ReadySources(ctx context.Context, notebook Notebook) (Sources, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT d.title, left(COALESCE(NULLIF(d.source_markdown, ''), d.content), 6000), count(*) OVER ()
		FROM notebook_sources AS ns
		JOIN documents AS d ON d.id = ns.document_id AND d.user_id = ns.user_id
		WHERE ns.notebook_id = $1 AND ns.user_id = $2::uuid AND ns.enabled
		  AND d.status ->> 'state' = 'ready'
		ORDER BY ns.added_at, ns.id`, notebook.ID, notebook.UserID)
	if err != nil {
		return Sources{}, fmt.Errorf("reading the notebook's sources: %w", err)
	}
	defer rows.Close()
	var sources Sources
	for rows.Next() {
		var (
			source excerpt
			total  int
		)
		if err := rows.Scan(&source.title, &source.text, &total); err != nil {
			return Sources{}, err
		}
		sources.Ready = total
		if len(sources.First) < maxSources {
			sources.First = append(sources.First, source)
		}
	}
	return sources, rows.Err()
}

// outputSeparator is how a Studio output is titled after its notebook:
// "Podcast: Untitled notebook".
const outputSeparator = ": "

func (s Store) Rename(ctx context.Context, notebook Notebook, naming Naming) (bool, error) {
	renamed := false
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE notebooks
			SET title = $3, auto_title = $4,
			    icon = CASE WHEN icon_source = 'auto' AND $5 <> '' THEN $5 ELSE icon END
			WHERE id = $1 AND user_id = $2::uuid AND title_source = 'auto'
			  AND CASE WHEN jsonb_typeof(auto_title -> 'runs') = 'number'
			           THEN (auto_title ->> 'runs')::numeric ELSE 0 END = $6`,
			notebook.ID, notebook.UserID, naming.Title, naming.AutoTitle, naming.Icon, naming.ExpectedRuns)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		renamed = true
		return retitleOutputs(ctx, tx, notebook, naming)
	})
	return renamed, err
}

// retitleOutputs gives the notebook's Studio outputs that still carry the
// old name (or a default one) the new name, and the artifact document a
// finished output was saved as, while that still has the output's title.
// An output the person retitled is left alone (app/notebooks/output_titles.py).
func retitleOutputs(ctx context.Context, tx pgx.Tx, notebook Notebook, naming Naming) error {
	old := append([]string{naming.OldTitle}, defaultTitles...)
	rows, err := tx.Query(ctx, `
		SELECT id, title, deliverable_job_id FROM notebook_outputs
		WHERE notebook_id = $1 AND user_id = $2::uuid`, notebook.ID, notebook.UserID)
	if err != nil {
		return err
	}
	type output struct {
		id    int64
		title string
		job   *int64
	}
	var outputs []output
	for rows.Next() {
		var o output
		if err := rows.Scan(&o.id, &o.title, &o.job); err != nil {
			rows.Close()
			return err
		}
		outputs = append(outputs, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, o := range outputs {
		head, tail, found := strings.Cut(o.title, outputSeparator)
		if !found || head == "" || !contains(old, tail) || tail == naming.Title {
			continue
		}
		retitled := firstRunes(head+outputSeparator+naming.Title, 200)
		if _, err := tx.Exec(ctx, `UPDATE notebook_outputs SET title = $3 WHERE id = $1 AND user_id = $2::uuid`,
			o.id, notebook.UserID, retitled); err != nil {
			return err
		}
		if o.job == nil {
			continue
		}
		if _, err := tx.Exec(ctx, `
			UPDATE documents SET title = $4
			WHERE user_id = $2::uuid AND title = $3 AND id IN (
			    SELECT a.document_id FROM artifacts AS a
			    JOIN deliverable_jobs AS j ON j.artifact_id = a.id
			    WHERE j.id = $1 AND j.user_id = $2::uuid)`,
			*o.job, notebook.UserID, o.title, retitled); err != nil {
			return err
		}
	}
	return nil
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
