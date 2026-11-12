package tool

import (
	"context"
	"fmt"
)

// Page returns one page of a tool listing, and the cursor of the page after
// it. An empty next cursor means this page was the last. The first page is
// asked for with an empty cursor.
type Page func(ctx context.Context, cursor string) (tools []Tool, next string, err error)

// Collect reads a paged tool listing to its end and returns every tool.
//
// Remote tool servers page their listings, and a client that reads only the
// first page works with every small server and silently loses tools on a
// large one: nothing fails, the model is simply never offered them. So the
// only way this package offers to read a listing is the one that follows
// the cursor.
//
// maxPages is the caller's limit on how many pages one server may make it
// read. Reaching it is an error, not a short list: a list cut off at a limit
// is the same silent loss in another place. A server that answers with a
// cursor it has already given would be read forever, and is an error too.
func Collect(ctx context.Context, page Page, maxPages int) ([]Tool, error) {
	if page == nil {
		return nil, fmt.Errorf("tool: Collect needs a function that reads a page")
	}
	if maxPages < 1 {
		return nil, fmt.Errorf("tool: Collect needs a page limit of at least 1, got %d", maxPages)
	}
	var (
		all    []Tool
		cursor string
		seen   = map[string]struct{}{}
	)
	for n := 1; ; n++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		tools, next, err := page(ctx, cursor)
		if err != nil {
			return nil, fmt.Errorf("tool: read page %d of the listing: %w", n, err)
		}
		all = append(all, tools...)
		if next == "" {
			return all, nil
		}
		if _, again := seen[next]; again {
			return nil, fmt.Errorf("tool: the listing gave the cursor %q a second time on page %d; it would never end", next, n)
		}
		if n == maxPages {
			return nil, fmt.Errorf("tool: the listing has more than %d pages, the limit set for it", maxPages)
		}
		seen[next] = struct{}{}
		cursor = next
	}
}
