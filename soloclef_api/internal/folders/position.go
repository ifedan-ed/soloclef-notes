package folders

import (
	"context"
	"fmt"
	"net/http"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/fractionalindex"
)

// MaxDepth is how deep a folder may sit: a top-level folder is at depth 1.
const MaxDepth = 8

// positions decides where a folder sits among its siblings and how deep it
// may sit, for one account.
type positions struct {
	Folders Rows
	UserID  string
}

// last is a position after the parent's last folder, or the first position
// of all under a parent with none.
func (p positions) last(ctx context.Context, parentID *int64) (string, error) {
	last, err := p.Folders.LastSiblingPosition(ctx, p.UserID, parentID)
	if err != nil {
		return "", err
	}
	return fractionalindex.KeyBetween(last, nil)
}

// checkDepth refuses to place a folder, with the levels hanging below it,
// under a parent where it would pass MaxDepth. A nil parent is the top
// level, whose depth is 0 without a query.
func (p positions) checkDepth(ctx context.Context, parentID *int64, subtreeDepth int) (*refusal, error) {
	parentDepth := 0
	if parentID != nil {
		depth, err := p.Folders.Depth(ctx, p.UserID, *parentID)
		if err != nil {
			return nil, err
		}
		parentDepth = depth
	}
	if total := parentDepth + 1 + subtreeDepth; total > MaxDepth {
		return &refusal{Status: http.StatusBadRequest, Message: tooDeep(total)}, nil
	}
	return nil, nil
}

// tooDeep names both the limit and the depth the request would have
// reached.
func tooDeep(total int) string {
	return fmt.Sprintf(
		"Maximum folder nesting depth is %d. This operation would result in depth %d.",
		MaxDepth, total)
}
