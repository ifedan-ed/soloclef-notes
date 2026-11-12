package documentlifecycle

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/celeryqueue"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/connectorparser"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/documentdeletion"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/documentreindex"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/indexqueue"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/uploads"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/uploadvision"
	"git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// Dependencies is everything the routes are built from. Every field is
// required; none has a default, because each is a choice of the deployment.
type Dependencies struct {
	Pool *pgxpool.Pool
	// Queue is the tier's producer (internal/celeryqueue). The index job
	// names its own queue on every message, so the producer's default queue
	// does not matter to it.
	Queue celeryqueue.Enqueuer
	// Files is every store the deployment gave settings for. New files go
	// to the configured one (MinIO by default); a delete removes each file
	// from the store its row names.
	Files *filestore.Stores
	// Parsers is the registry soloclef_ingest/parserconfig.Load builds from
	// the same SOLOCLEF_PARSER_* settings the indexing worker reads.
	Parsers parserchoice.Registry
	// Vectors is the vector store the indexing worker writes chunks' points
	// to; a delete removes them.
	Vectors documentdeletion.Qdrant
	// Vision is the rule for "may this account switch use_vision_llm on"
	// (internal/uploadvision), given to the routes that take the switch and
	// to the one that tells the dialogs to offer it.
	Vision uploadvision.Vision
	// Limits bound an upload, and are what the capabilities route reports.
	Limits parserchoice.Limits
	// UploadStall is how long an uploading client may send nothing before
	// it is dropped (and how long an answer may wait to be accepted). It
	// replaces the server's ReadTimeout for the upload route only.
	UploadStall time.Duration
	Errors      errorenvelope.Writer
	Logger      *slog.Logger
}

// check names the first dependency that is missing or unusable.
func (d Dependencies) check() error {
	switch {
	case d.Pool == nil:
		return errors.New("documentlifecycle: no database pool")
	case d.Queue == nil:
		return errors.New("documentlifecycle: no queue to publish index jobs to")
	case d.Files == nil:
		return errors.New("documentlifecycle: no file stores")
	case d.Parsers == nil:
		return errors.New("documentlifecycle: no parser registry")
	case d.Logger == nil:
		return errors.New("documentlifecycle: no logger")
	case d.Vision.Check() != nil:
		return d.Vision.Check()
	case d.Limits.MaxFileBytes < 1:
		return fmt.Errorf("documentlifecycle: the per-file upload limit must be positive, got %d bytes", d.Limits.MaxFileBytes)
	case d.Limits.MaxFiles < 1:
		return fmt.Errorf("documentlifecycle: the files-per-upload limit must be positive, got %d", d.Limits.MaxFiles)
	case d.UploadStall <= 0:
		return fmt.Errorf("documentlifecycle: the upload stall timeout must be positive, got %v", d.UploadStall)
	}
	return d.Vectors.Check()
}

// Register mounts the seven routes on the API, or says what is missing.
func Register(api huma.API, d Dependencies) error {
	if err := d.check(); err != nil {
		return err
	}
	// The store new files are written to. A deployment whose configured
	// backend cannot be written by this process is refused here, at
	// start-up, not on the first upload.
	writer, err := d.Files.Writer()
	if err != nil {
		return err
	}
	indexing := indexqueue.Indexing{Queue: d.Queue, Marker: indexqueue.PostgresMarker{Pool: d.Pool}, Logger: d.Logger}
	vision := d.Vision

	if err := (parserchoice.CapabilitiesRoute{
		Parsers: d.Parsers, Limits: d.Limits, Vision: vision, Errors: d.Errors, Logger: d.Logger,
	}).Register(api); err != nil {
		return err
	}
	uploads.Route{
		Rows: uploads.PostgresRows{Pool: d.Pool}, Files: writer, Parsers: d.Parsers, Vision: vision,
		Indexing: indexing, Limits: d.Limits, Stall: d.UploadStall, Errors: d.Errors, Logger: d.Logger,
	}.Register(api)
	// The folder upload is the upload with a folder for each file, and is
	// given the very same parts: one store, one registry, one queue, one
	// set of limits.
	uploads.FolderRoute{
		Rows: uploads.PostgresRows{Pool: d.Pool}, Files: writer, Parsers: d.Parsers, Vision: vision,
		Indexing: indexing, Limits: d.Limits, Stall: d.UploadStall, Errors: d.Errors, Logger: d.Logger,
	}.Register(api)
	documentreindex.Route{
		Rows: documentreindex.PostgresRows{Pool: d.Pool}, Parsers: d.Parsers,
		Indexing: indexing, Errors: d.Errors, Logger: d.Logger,
	}.Register(api)
	removed := documentdeletion.PostgresRows{Pool: d.Pool}
	documentdeletion.Route{
		Owners: removed, Errors: d.Errors, Logger: d.Logger,
		Removal: documentdeletion.Removal{Rows: removed, Vectors: d.Vectors, Files: d.Files},
	}.Register(api)
	connectorparser.Routes{
		Rows: connectorparser.PostgresRows{Pool: d.Pool}, Parsers: d.Parsers, Errors: d.Errors, Logger: d.Logger,
	}.Register(api)
	return nil
}

// Served is the method-and-path pairs Register mounts, for cmd/api's
// servedRoutes(). None of them belongs in `withBodies`: the two uploads must
// not be buffered, the connector parser's PUT reads its own small body, and
// the rest have none.
func Served() []string {
	return []string{
		http.MethodGet + " " + parserchoice.CapabilitiesPath,
		http.MethodPost + " " + uploads.Path,
		http.MethodPost + " " + uploads.FolderPath,
		http.MethodPost + " " + documentreindex.Path,
		http.MethodDelete + " " + documentdeletion.Path,
		http.MethodGet + " " + connectorparser.Path,
		http.MethodPut + " " + connectorparser.Path,
	}
}
