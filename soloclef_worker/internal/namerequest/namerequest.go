// Package namerequest asks the worker to name the notebooks a document has
// just become readable for.
//
// A notebook still called "Untitled notebook" names itself from its first
// ready source (internal/notebookname is the job that does it). Most sources
// are added while their document is still being read: an upload, a Drive or
// Nextcloud import made from a notebook's Add sources dialog links the
// document at once and the indexer makes it ready seconds or minutes later.
// Nothing else is there at that moment, so the indexer, which is, publishes
// the naming once the document is ready, for every notebook that reads it
// and may still name itself.
//
// It publishes exactly the message the API tier publishes when a chat's
// mention adds a readable source (soloclef_api/internal/chat/mentions.go):
// `notebook_autoname(notebook_id, "source", null)`, on the deployment's
// default queue, a few seconds late. The delay lets a batch that finishes
// together be named from more than its first file; the job itself decides
// whether a name is still owed, so a message published twice, or for a
// notebook renamed meanwhile, names nothing.
//
// Naming is a courtesy. A lookup or a publish that fails is logged and the
// document stays ready: indexing never fails or waits for it.
package namerequest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_celery/routing"
)

// Task is the worker's naming job (notebookname.Task). It is repeated
// rather than imported so the indexer does not link the job's model client;
// the test pins the two together.
const Task = "notebook_autoname"

// Settle is how long the naming waits after a document is ready: the
// API's own wait (autonameSettle) and the Python tier's before it.
const Settle = 4 * time.Second

// maxRuns is how many times a notebook names itself in all
// (notebookname's maxRuns). A notebook that has used them up is not even
// asked about.
const maxRuns = 2

// Notebooks finds the notebooks that may name themselves from a document.
type Notebooks interface {
	ToName(ctx context.Context, documentID int64) ([]int64, error)
}

// Sender publishes one message (produce.Producer).
type Sender interface {
	Send(ctx context.Context, message produce.Message) (string, error)
}

// Requester publishes the naming for a document that became ready.
type Requester struct {
	Notebooks Notebooks
	Sender    Sender
	Log       *slog.Logger
}

// New builds the requester over the indexer's own database pool and Redis
// client, publishing to the deployment's default queue.
func New(pool *pgxpool.Pool, rdb redis.UniversalClient, defaultQueue string, log *slog.Logger) (*Requester, error) {
	switch {
	case pool == nil || rdb == nil:
		return nil, errors.New("namerequest: no database or no Redis client")
	case defaultQueue == "":
		return nil, errors.New("namerequest: no default queue")
	case log == nil:
		return nil, errors.New("namerequest: no logger")
	}
	replyTo, err := produce.NewID()
	if err != nil {
		return nil, err
	}
	return &Requester{
		Notebooks: Store{Pool: pool},
		Sender: produce.Producer{
			Broker: redisBroker{rdb: rdb}, Routes: routing.Table{Default: defaultQueue},
			Origin: produce.Origin(), ReplyTo: replyTo,
		},
		Log: log,
	}, nil
}

// Message is the naming of one notebook from its sources.
func Message(notebookID int64) produce.Message {
	return produce.Message{Task: Task, Args: []any{notebookID, "source", nil}, Countdown: Settle}
}

// DocumentReady publishes the naming of every notebook that reads the
// document and may still name itself. It never fails the caller.
func (r *Requester) DocumentReady(ctx context.Context, documentID int64) {
	notebooks, err := r.Notebooks.ToName(ctx, documentID)
	if err != nil {
		r.Log.Warn("could not look up the notebooks to name from a ready document", "document", documentID, "error", err)
		return
	}
	for _, notebook := range notebooks {
		if _, err := r.Sender.Send(ctx, Message(notebook)); err != nil {
			r.Log.Warn("could not ask for a notebook to be named", "document", documentID, "notebook", notebook, "error", err)
		}
	}
}

// Store is Notebooks over Postgres.
type Store struct {
	Pool *pgxpool.Pool
}

// ToName is one indexed read (ix_notebook_sources_document_id). It asks
// what the job will check again under its own lock, so that the usual
// answer, a document no self-naming notebook reads, publishes nothing: the
// title still the app's, and runs left. A source turned off is not read by
// the naming, so it does not ask for one either.
func (s Store) ToName(ctx context.Context, documentID int64) ([]int64, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT n.id
		FROM notebook_sources AS ns
		JOIN notebooks AS n ON n.id = ns.notebook_id AND n.user_id = ns.user_id
		WHERE ns.document_id = $1 AND ns.enabled
		  AND n.title_source = 'auto'
		  AND CASE WHEN jsonb_typeof(n.auto_title -> 'runs') = 'number'
		           THEN (n.auto_title ->> 'runs')::numeric ELSE 0 END < $2
		ORDER BY n.id`, documentID, maxRuns)
	if err != nil {
		return nil, fmt.Errorf("reading the notebooks to name: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

type redisBroker struct{ rdb redis.UniversalClient }

func (b redisBroker) LPush(ctx context.Context, key, value string) error {
	return b.rdb.LPush(ctx, key, value).Err()
}
