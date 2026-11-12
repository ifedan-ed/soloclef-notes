package folders

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
)

// opening is how the seven routes on `/folders/{folder_id}` begin: say what
// is wrong with the request, then read the folder for the caller.
//
// The read is the access check. It names the caller's account, so a folder
// of another account comes back as nothing and is answered with the same
// 404 as a folder that was never made; no route here can tell a stranger
// whether an id is in use.
//
// A bad path and a bad body are reported together, the path first, so a
// client that got both wrong learns both from one answer.
type opening struct {
	Folders Rows
	Errors  errorenvelope.Writer
}

// open validates and reads. The last result is false when it has already
// answered the request.
func (o opening) open(
	ctx huma.Context,
	response http.ResponseWriter,
	requestID string,
	fail failures,
	bodyFailures []errorenvelope.Failure,
) (principal.Principal, *Folder, bool) {
	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		// Unreachable behind the gate; a 500 and not an unauthenticated
		// write if the wiring is ever wrong.
		fail.fail(ctx, response, requestID, "the route ran without an authenticated caller", nil)
		return caller, nil, false
	}

	folderID, pathFailure, rangeErr := requestnumber.Parse(ctx.Param("folder_id"), "path", "folder_id")
	if !reportInvalid(o.Errors, response, requestID, pathFailure, bodyFailures) {
		return caller, nil, false
	}
	if rangeErr != nil {
		// A number no id column can hold names no folder.
		o.Errors.Write(response, http.StatusNotFound, NotFound, requestID)
		return caller, nil, false
	}

	folder, err := o.Folders.Folder(ctx.Context(), caller.UserID, folderID)
	if err != nil {
		fail.fail(ctx, response, requestID, "could not read the folder", err)
		return caller, nil, false
	}
	if folder == nil {
		o.Errors.Write(response, http.StatusNotFound, NotFound, requestID)
		return caller, nil, false
	}
	return caller, folder, true
}

// reportInvalid answers 422 with the path's failure and the body's, in
// that order, and reports whether the request was valid.
func reportInvalid(
	errors errorenvelope.Writer,
	response http.ResponseWriter,
	requestID string,
	pathFailure *errorenvelope.Failure,
	bodyFailures []errorenvelope.Failure,
) bool {
	if pathFailure == nil && len(bodyFailures) == 0 {
		return true
	}
	combined := make([]errorenvelope.Failure, 0, 1+len(bodyFailures))
	if pathFailure != nil {
		combined = append(combined, *pathFailure)
	}
	combined = append(combined, bodyFailures...)
	errors.WriteValidation(response, requestID, combined)
	return false
}
