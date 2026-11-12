package tool_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// pages is a listing in three pages, the shape of a remote tool server's.
func pages(calls *[]string) tool.Page {
	listing := map[string]struct {
		names []string
		next  string
	}{
		"":   {[]string{"a", "b"}, "p2"},
		"p2": {[]string{"c"}, "p3"},
		"p3": {[]string{"d"}, ""},
	}
	return func(_ context.Context, cursor string) ([]tool.Tool, string, error) {
		*calls = append(*calls, cursor)
		page := listing[cursor]
		var out []tool.Tool
		for _, name := range page.names {
			out = append(out, tool.Tool{Name: name})
		}
		return out, page.next, nil
	}
}

func TestCollectFollowsTheCursorToTheLastPage(t *testing.T) {
	var calls []string
	got, err := tool.Collect(context.Background(), pages(&calls), 10)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range got {
		names = append(names, tl.Name)
	}
	if strings.Join(names, ",") != "a,b,c,d" || strings.Join(calls, ",") != ",p2,p3" {
		t.Fatalf("tools %v from pages %q", names, calls)
	}
}

func TestCollectNeverReturnsAShortListAsIfItWereWhole(t *testing.T) {
	ctx := context.Background()
	var calls []string
	if _, err := tool.Collect(ctx, pages(&calls), 2); err == nil || !strings.Contains(err.Error(), "more than 2 pages") {
		t.Fatalf("a listing longer than the limit: %v", err)
	}
	// A server whose cursor goes round in a circle.
	loop := func(_ context.Context, cursor string) ([]tool.Tool, string, error) {
		if cursor == "x" {
			return nil, "y", nil
		}
		return nil, "x", nil
	}
	if _, err := tool.Collect(ctx, loop, 100); err == nil || !strings.Contains(err.Error(), "second time") {
		t.Fatalf("a circular listing: %v", err)
	}
	down := errors.New("server down")
	failing := func(context.Context, string) ([]tool.Tool, string, error) { return nil, "", down }
	if _, err := tool.Collect(ctx, failing, 5); !errors.Is(err, down) {
		t.Fatalf("a failed page: %v", err)
	}
	stopped, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := tool.Collect(stopped, pages(&calls), 5); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled listing: %v", err)
	}
	if _, err := tool.Collect(ctx, nil, 5); err == nil {
		t.Fatal("no page function was accepted")
	}
	if _, err := tool.Collect(ctx, pages(&calls), 0); err == nil {
		t.Fatal("a zero page limit was accepted")
	}
}
