package documents

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Progress is one document as the status poll reads it.
type Progress struct {
	ID           int64
	Title        string
	DocumentType string
	// Status is documents.status as the column's text, nil for SQL NULL. The
	// column is jsonb, and what the upload flow writes there is
	// {"state": …} with a "reason" beside a failure.
	Status []byte
}

// Statuses reads what the status poll reads.
type Statuses interface {
	// ProgressOf is the account's documents among the ids given, in id
	// order.
	ProgressOf(ctx context.Context, userID string, documentIDs []int64) ([]Progress, error)
}

// StatusRoute serves GET /documents/status (get_documents_status): the state
// of each of a list of documents, which the chat's upload flow polls while a
// file is being processed.
//
// # Whose documents
//
// The caller's own, and no others: the statement names the owner, so an id
// that is another account's is simply not among the rows. The poll says
// nothing about it, exactly as it says nothing about an id never used.
//
// # The order
//
// The rows come back by id (`ORDER BY documents.id`), not in the order the
// ids were asked in.
//
// # What is refused
//
// A missing `document_ids` is the 422; a piece of it that is not a number
// is the 400 that names the piece.
//
// Every 500 of this route carries the CORS headers (failure.go), including
// the one a stored status of the wrong shape earns.
type StatusRoute struct {
	Statuses Statuses
	Errors   errorenvelope.Writer
	Logger   *slog.Logger
}

// Register adds the operation. Neither parameter is declared to Huma: both
// refusals have to be the Python tier's 422 envelope, word for word, and the
// query is read as Starlette reads it (starlette_query.go).
func (r StatusRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-documents-status",
		Method:      http.MethodGet,
		Path:        StatusPath,
		Summary:     "The processing state of a list of documents",
		Description: "The same response as the Python tier's " +
			"GET /api/v1/documents/status, byte for byte.",
		Tags: []string{"documents"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r StatusRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := failures{ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestID, what: "the status of a list of documents could not be served"}

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		failed.uncaught(noCaller, nil)
		return
	}

	requested := ctx.URL()
	query := readStarletteQuery(requested.RawQuery)

	invalid := make([]errorenvelope.Failure, 0, 1)
	listed, named := query.value("document_ids")
	if !named {
		invalid = append(invalid, errorenvelope.Failure{
			Location: []string{"query", "document_ids"},
			Message:  requestbody.FieldRequired,
		})
	}
	if len(invalid) > 0 {
		r.Errors.WriteValidation(response, requestID, invalid)
		return
	}

	documentIDs, tooWide, unreadable := readDocumentIDs(listed)
	if unreadable != nil {
		r.Errors.Write(response, http.StatusBadRequest, invalidDocumentIDPrefix+*unreadable, requestID)
		return
	}

	// `if not parsed_ids: return DocumentStatusBatchResponse(items=[])` --
	// before the statement, so an empty list asks the database nothing.
	progress := []Progress{}
	var err error
	if len(documentIDs) > 0 || tooWide {
		// An id past int64 is one Python holds and hands to the driver,
		// which refuses it; this tier cannot hold it, and fails where that
		// statement would have been issued.
		if tooWide {
			failed.caught(documentTooWide, errTooWide)
			return
		}
		progress, err = r.Statuses.ProgressOf(ctx.Context(), caller.UserID, documentIDs)
		if err != nil {
			failed.caught("could not read the documents' status", err)
			return
		}
	}

	rendered, err := buildStatuses(progress)
	if err != nil {
		failed.caught("a stored status is not one the response model accepts", err)
		return
	}
	body, err := pythonjson.Marshal(rendered)
	if err != nil {
		failed.uncaught(answerUnrendered, err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

// readDocumentIDs is the route's own loop over `document_ids.split(",")`:
// each piece is stripped, an empty piece is skipped, and the first piece
// that `int()` refuses ends the request with a 400 naming it.
//
// The whole list is read before anything is done with it, so a list holding
// both a number too wide for any column and a word is the word's 400, not
// the number's 500. tooWide reports a list every piece of which was read and
// at least one of which is past int64.
func readDocumentIDs(listed string) (documentIDs []int64, tooWide bool, unreadable *string) {
	for _, piece := range strings.Split(listed, ",") {
		value := pythontext.Strip(piece)
		if value == "" {
			continue
		}
		read, readable := pythonInt(value)
		if !readable {
			return nil, false, &value
		}
		if read.tooWide() {
			tooWide = true
			continue
		}
		documentIDs = append(documentIDs, read.value)
	}
	return documentIDs, tooWide, nil
}

// errStatusShape is a stored `status` the route cannot build its models
// from. Nothing this application writes has that shape; the column is jsonb
// and can hold anything.
var errStatusShape = errors.New("documents: a stored status is not one DocumentStatusSchema accepts")

// buildStatuses renders DocumentStatusBatchResponse: `items`, each a
// DocumentStatusItemRead of id, title, document_type and status.
func buildStatuses(progress []Progress) (*pythonjson.Object, error) {
	items := make([]any, 0, len(progress))
	for _, document := range progress {
		status, err := buildStatus(document.Status)
		if err != nil {
			return nil, err
		}
		item := pythonjson.NewObject()
		item.Set("id", document.ID)
		item.Set("title", document.Title)
		item.Set("document_type", document.DocumentType)
		item.Set("status", status)
		items = append(items, item)
	}
	body := pythonjson.NewObject()
	body.Set("items", items)
	return body, nil
}

// buildStatus is
//
//	DocumentStatusSchema(
//	    state=(doc.status or {}).get("state", "ready"),
//	    reason=(doc.status or {}).get("reason"),
//	)
//
// read one step at a time, because each step has an answer of its own:
//
//   - `doc.status or {}`: anything Python calls false is an empty mapping --
//     SQL NULL, JSON null, {}, [], "", 0, 0.0 and false. A document with no
//     status at all is therefore "ready".
//   - `.get(…)`: anything else that is not an object has no `.get`, which is
//     an AttributeError and the route's 500.
//   - `"state"` falls back to "ready" only when the key is absent. A key
//     that is present and null is None, which the model refuses.
//   - the model: `state: str` and `reason: str | None`. pydantic does not
//     turn a number or a boolean into a string, so either is a 500. Any
//     string is accepted for the state, declared or not; the comment beside
//     the field lists three and nothing enforces them.
func buildStatus(stored []byte) (*pythonjson.Object, error) {
	fields := pythonjson.NewObject()
	if len(stored) > 0 {
		value, err := pythonjson.Decode(stored)
		if err != nil {
			return nil, err
		}
		if !pythonFalse(value) {
			object, isObject := value.(*pythonjson.Object)
			if !isObject {
				return nil, errStatusShape
			}
			fields = object
		}
	}

	state := any("ready")
	if held, present := fields.Get("state"); present {
		state = held
	}
	if _, isText := state.(string); !isText {
		return nil, errStatusShape
	}
	reason := fields.GetOrNil("reason")
	if _, isText := reason.(string); reason != nil && !isText {
		return nil, errStatusShape
	}

	status := pythonjson.NewObject()
	status.Set("state", state)
	status.Set("reason", reason)
	// The indexing worker's progress, which the Python model dropped: while
	// a document is processing its status carries a "stage" (parsing,
	// chunking, embedding, indexing or retrying; indexstore says which and
	// when), so a slow upload can say what it is waiting on. It is answered
	// only when it is there and means something -- a text, on a processing
	// document -- so every other status is the two keys it always was.
	if stage, isText := fields.GetOrNil("stage").(string); isText && stage != "" && state == "processing" {
		status.Set("stage", stage)
	}
	return status, nil
}

// pythonFalse is `not value` for a value read out of a json column.
func pythonFalse(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case bool:
		return !typed
	case string:
		return typed == ""
	case float64:
		// 0.0 and -0.0 both.
		return typed == 0
	case pythonjson.RawInt:
		// An integer of any width, as its digits: zero is "0" or "-0".
		return strings.Trim(string(typed), "-0") == ""
	case []any:
		return len(typed) == 0
	case *pythonjson.Object:
		return typed.Len() == 0
	}
	return false
}
