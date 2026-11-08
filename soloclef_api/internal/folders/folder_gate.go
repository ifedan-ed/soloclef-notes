package folders

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
)

// folderGate is the opening the seven routes on `/folders/{folder_id}` share —
// the two reads and the five writes: validate what the request says, load the
// row, then ask whether the caller may act on it.
//
// Loading before gating is what makes an absent folder a 404 and one in another
// workspace a 403, which is the Python routes' own order and the oracle under
// "Three more defects the port exposed".
//
// The order of the first two is FastAPI's rather than this tier's. A path
// parameter and a body are validated together before the route's own code runs,
// so a request with both wrong is answered with both failures in one envelope
// and the path's first — and a path id too wide for the column is a 500 only
// once the body has validated, because Python holds the integer and the query
// is what fails on it. A read passes no body failures and takes the same path.
type folderGate struct {
	Folders Store
	Access  Access
	Errors  errorenvelope.Writer
}

// open validates and loads. The second result is false when it has already
// answered the request.
func (g folderGate) open(
	ctx huma.Context,
	response http.ResponseWriter,
	requestID string,
	fail failures,
	bodyFailures []errorenvelope.Failure,
	permission, denial string,
) (principal.Principal, *Folder, bool) {
	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		// Unreachable behind the gate; a 500 rather than an unauthenticated
		// write if the wiring is ever wrong.
		fail.fail(ctx, response, requestID, "the route ran without an authenticated caller", nil)
		return caller, nil, false
	}

	folderID, pathFailure, rangeErr := parseFolderID(ctx)
	if pathFailure != nil || len(bodyFailures) > 0 {
		combined := make([]errorenvelope.Failure, 0, 1+len(bodyFailures))
		if pathFailure != nil {
			combined = append(combined, *pathFailure)
		}
		combined = append(combined, bodyFailures...)
		g.Errors.WriteValidation(response, requestID, combined)
		return caller, nil, false
	}
	if rangeErr != nil {
		fail.fail(ctx, response, requestID, "a parameter is outside the range a request can use", rangeErr)
		return caller, nil, false
	}

	folder, err := g.Folders.Folder(ctx.Context(), folderID)
	if err != nil {
		fail.fail(ctx, response, requestID, "could not read the folder", err)
		return caller, nil, false
	}
	if folder == nil {
		g.Errors.Write(response, http.StatusNotFound, NotFound, requestID)
		return caller, nil, false
	}

	refusal, err := checkGates(ctx.Context(), g.Access, caller,
		folder.WorkspaceID, permission, denial)
	if err != nil {
		fail.fail(ctx, response, requestID, "could not read the caller's standing in the workspace", err)
		return caller, nil, false
	}
	if refusal != nil {
		g.Errors.Write(response, refusal.Status, refusal.Message, requestID)
		return caller, nil, false
	}
	return caller, folder, true
}
