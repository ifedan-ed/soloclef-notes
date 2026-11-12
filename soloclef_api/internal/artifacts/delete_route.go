package artifacts

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/documentdeletion"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
)

// Document is the document an artifact stands on, as the delete needs it:
// its id, and its status column exactly as stored, so that the same bytes can
// be compared against and put back.
type Document struct {
	ID     int64
	Status []byte
}

// Deletions is the delete's read and its one write of its own.
type Deletions interface {
	DocumentOf(ctx context.Context, userID string, artifactID int64) (*Document, error)
	// MarkDeleting reports false when the document no longer has the status
	// it was read with.
	MarkDeleting(ctx context.Context, userID string, document Document) (bool, error)
}

// Remover removes a document and everything stored for it
// (documentdeletion.Removal).
type Remover interface {
	Remove(ctx context.Context, documentID int64) error
}

// removalTimeout bounds one removal, which runs outside the request's
// context (see serve) and so needs a limit of its own. It is the document
// delete's figure: the work is the same work.
const removalTimeout = 2 * time.Minute

// DeleteRoute serves the delete.
//
// An artifact is deleted by deleting the document it stands on, and the
// artifact row goes with it by the foreign key's cascade. The route does
// that itself, before it answers, with the removal DELETE
// /api/v1/documents/{document_id} uses (documentdeletion.Removal): the
// document's vectors, its chunks, its stored files -- the audio, the video,
// the deck -- and then its row. It used to mark the document and hand the
// rest to a Python task on the queue, so a 204 meant "queued", the vectors
// were left for a sweeper, and a deployment with no Python worker deleted
// nothing at all.
//
// The steps:
//
//  1. The document is claimed: marked "deleting" over the status that was
//     just read and no other (MarkDeleting). A document that something
//     began working on in between is not claimed, and the caller is told to
//     try again.
//  2. The removal runs. Every step of it can be repeated, so a removal that
//     stops partway leaves the document marked, out of search, and the
//     same request finishes it: there is nothing to put back, which is why
//     the old "restore the status" step is gone.
//
// What it refuses is a document still pending or being processed. The
// document delete no longer refuses those, because the indexing worker
// checks the marker before it writes. An artifact's document is written by
// the generation job instead (the podcast, the video, the deck), and
// nothing has shown that job checks the marker, so deleting underneath it
// could leave files the job stored after the removal had listed them. A
// document already marked "deleting" is not refused: that is a removal an
// earlier request did not finish, and this one carries it on.
type DeleteRoute struct {
	Deletions Deletions
	Removal   Remover
	Errors    errorenvelope.Writer
	Logger    *slog.Logger
}

// Register adds the operation; the path parameters are strings for the reason
// ListRoute.Register gives.
func (r DeleteRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "delete-artifact",
		Method:      http.MethodDelete,
		Path:        DeletePath,
		Summary:     "Delete an artifact",
		Tags:        []string{"artifacts"},
	}, func(ctx context.Context, _ *struct {
		ArtifactID string `path:"artifact_id" doc:"The artifact's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r DeleteRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := func(reason string, cause error) {
		writeFailure(ctx, r.Errors, r.Logger, response, requestID,
			"the artifact could not be deleted", reason, cause)
	}

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		// Unreachable behind the gate; a 500 rather than an unauthenticated
		// delete if the wiring is ever wrong.
		failed(noCaller, nil)
		return
	}

	failures := make([]errorenvelope.Failure, 0, 1)
	artifact := readNumber(ctx.Param("artifact_id"), &failures, "path", "artifact_id")
	if len(failures) > 0 {
		r.Errors.WriteValidation(response, requestID, failures)
		return
	}

	if artifact.tooWide {
		failed(artifactTooWide, requestnumber.ErrOutOfRange)
		return
	}
	document, err := r.Deletions.DocumentOf(ctx.Context(), caller.UserID, artifact.value)
	if err != nil {
		failed("could not read the artifact", err)
		return
	}
	if document == nil {
		// Unknown, or another account's: the same answer either way, and
		// nothing of somebody else's artifact is touched.
		r.Errors.Write(response, http.StatusNotFound, artifactAbsent, requestID)
		return
	}
	// A status that names no state -- NULL, or something that is not an
	// object -- is a document nothing is working on, and may be deleted.
	state, _ := objectOf(decodeColumn(document.Status)).GetOrNil("state").(string)
	switch state {
	case "pending", "processing":
		r.Errors.Write(response, http.StatusConflict, beingGenerated, requestID)
		return
	case "deleting":
		// Already claimed by an earlier delete that did not finish. There
		// is nothing to compare and swap: the marker is there, and the
		// removal below carries on from wherever that one stopped.
	default:
		// The claim, committed -- over the status just read and no other.
		marked, err := r.Deletions.MarkDeleting(ctx.Context(), caller.UserID, *document)
		if err != nil {
			failed("could not mark the artifact's document for deletion", err)
			return
		}
		if !marked {
			r.Errors.Write(response, http.StatusConflict, changedMeanwhile, requestID)
			return
		}
	}

	// Not the request's context. A delete that has begun should finish even
	// if the person closes the tab: a document half removed is repaired by
	// asking again, but nobody asks again for a request they walked away
	// from.
	removing, done := context.WithTimeout(context.WithoutCancel(ctx.Context()), removalTimeout)
	defer done()
	started := time.Now()
	if err := r.Removal.Remove(removing, document.ID); err != nil {
		r.Logger.LogAttrs(ctx.Context(), slog.LevelError,
			"an artifact was not fully deleted; its document is left marked and the delete can be repeated",
			slog.String("request_id", requestID),
			slog.Int64("document_id", document.ID),
			slog.Duration("after", time.Since(started)),
			slog.String("error", err.Error()))
		r.Errors.Write(response, http.StatusServiceUnavailable, documentdeletion.Unfinished, requestID)
		return
	}
	r.Logger.LogAttrs(ctx.Context(), slog.LevelInfo, "artifact deleted",
		slog.String("request_id", requestID),
		slog.Int64("document_id", document.ID),
		slog.Duration("took", time.Since(started)))

	// 204 with no body, and told not to be cached.
	response.Header().Set("Cache-Control", privateNoStore)
	response.WriteHeader(http.StatusNoContent)
}
