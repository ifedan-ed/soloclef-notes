package folders

import (
	"context"
	"fmt"
	"net/http"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/fractionalindex"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/workspaceaccess"
)

// MaxDepth is app/services/folder_service.py's MAX_FOLDER_DEPTH.
const MaxDepth = 8

// positions is the half of app/services/folder_service.py this slice needs:
// where a folder sits among its siblings, and how deep it may sit.
type positions struct{ Folders Store }

// next is generate_folder_position. With neither neighbour named it appends
// after the last sibling; with one or both named it puts the key between them,
// and a key a client made up is the library's refusal and the route's
// sanitised 500 (internal/fractionalindex).
//
// The two branches are the Python helper's and the order matters: naming
// *either* neighbour skips the sibling query entirely, so a reorder asking for
// a position after `before_position` does not read the siblings at all.
func (p positions) next(
	ctx context.Context,
	workspaceID int64,
	parentID *int64,
	before, after *string,
) (string, error) {
	if before != nil || after != nil {
		return fractionalindex.KeyBetween(before, after)
	}
	last, err := p.Folders.LastSiblingPosition(ctx, workspaceID, parentID)
	if err != nil {
		return "", err
	}
	return fractionalindex.KeyBetween(last, nil)
}

// checkDepth is validate_folder_depth: placing a folder, with everything under
// it, below `parentID` must not pass MaxDepth. A nil parent is the root, whose
// depth is 0 without a query.
func (p positions) checkDepth(ctx context.Context, parentID *int64, subtreeDepth int) (*workspaceaccess.Refusal, error) {
	parentDepth := 0
	if parentID != nil {
		depth, err := p.Folders.Depth(ctx, *parentID)
		if err != nil {
			return nil, err
		}
		parentDepth = depth
	}
	total := parentDepth + 1 + subtreeDepth
	if total > MaxDepth {
		return &workspaceaccess.Refusal{
			Status:  http.StatusBadRequest,
			Message: tooDeep(total),
		}, nil
	}
	return nil, nil
}

// tooDeep is the refusal's wording, which names both the limit and the depth
// the operation would have reached.
func tooDeep(total int) string {
	return fmt.Sprintf(
		"Maximum folder nesting depth is %d. This operation would result in depth %d.",
		MaxDepth, total)
}
