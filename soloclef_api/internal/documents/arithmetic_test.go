package documents

import (
	"math"
	"math/big"
	"testing"
)

// The two pieces of arithmetic in this slice are Python expressions over
// integers that have no width, written here in int64 without forming any
// intermediate that could overflow. These tests work each expression out the
// way Python does -- in math/big -- over the values where an int64 version
// would go wrong, and compare.

var edges = []int64{0, 1, 2, 5, 11, 12, 13, 100, math.MaxInt32, math.MaxInt32 + 1,
	math.MaxInt64 - 1, math.MaxInt64}

func TestWindowBoundsAreTheUnboundedExpression(t *testing.T) {
	// A number past int64, as `chunk_window=99999999999999999999` is.
	beyond, _ := new(big.Int).SetString("99999999999999999999", 10)
	type window struct {
		held   number
		python *big.Int
	}
	windows := []window{{held: number{beyond: 1}, python: beyond}}
	for _, value := range edges {
		windows = append(windows, window{held: number{value: value}, python: big.NewInt(value)})
	}
	checked := 0
	for _, total := range edges {
		for _, before := range edges {
			// The cited chunk is one of the document's, so fewer chunks come
			// before it than there are -- except across two statements, when
			// a document may have lost some in between. Both are asked.
			for _, w := range windows {
				// start = max(0, cited_idx - chunk_window)
				wantStart := new(big.Int).Sub(big.NewInt(before), w.python)
				if wantStart.Sign() < 0 {
					wantStart.SetInt64(0)
				}
				// end = min(total_chunks, cited_idx + chunk_window + 1)
				wantEnd := new(big.Int).Add(big.NewInt(before), w.python)
				wantEnd.Add(wantEnd, big.NewInt(1))
				if wantEnd.Cmp(big.NewInt(total)) > 0 {
					wantEnd.SetInt64(total)
				}
				start, end := windowBounds(total, before, w.held)
				if big.NewInt(start).Cmp(wantStart) != 0 || big.NewInt(end).Cmp(wantEnd) != 0 {
					t.Errorf("total %d, before %d, window %s: got [%d, %d), Python works out [%s, %s)",
						total, before, w.python, start, end, wantStart, wantEnd)
				}
				checked++
			}
		}
	}
	if checked != len(edges)*len(edges)*(len(edges)+1) {
		t.Errorf("checked %d combinations", checked)
	}
}

func TestTheOffsetIsTheUnboundedProductOrAFailure(t *testing.T) {
	largest := big.NewInt(math.MaxInt64)
	for _, page := range edges {
		for _, size := range []int64{1, 2, 5, 20, 99, 100} {
			want := new(big.Int).Mul(big.NewInt(page), big.NewInt(size))
			got, representable := chunkPaging{page: number{value: page}, pageSize: size}.offset()
			switch {
			case want.Cmp(largest) > 0 && representable:
				t.Errorf("page %d × %d is %s, past int64, and was answered as %d", page, size, want, got)
			case want.Cmp(largest) <= 0 && (!representable || big.NewInt(got).Cmp(want) != 0):
				t.Errorf("page %d × %d is %s and was answered as %d (representable: %v)",
					page, size, want, got, representable)
			}
		}
	}
	// A page past int64 is a product past int64 whatever the size, and a
	// start_offset replaces the product without reading the page.
	wide := number{beyond: 1}
	if _, representable := (chunkPaging{page: wide, pageSize: 1}).offset(); representable {
		t.Error("a page past int64 was multiplied")
	}
	seven := number{value: 7}
	if got, representable := (chunkPaging{page: wide, pageSize: 20, startOffset: &seven}).offset(); !representable || got != 7 {
		t.Errorf("start_offset=7 beside a page past int64: got %d, %v", got, representable)
	}
	if _, representable := (chunkPaging{pageSize: 20, startOffset: &wide}).offset(); representable {
		t.Error("a start_offset past int64 was used")
	}
}

func TestHasMoreDoesNotOverflow(t *testing.T) {
	for _, c := range []struct {
		offset   int64
		returned int
		total    int64
		want     bool
	}{
		{0, 5, 12, true},
		{5, 5, 12, true},
		{10, 2, 12, false},
		{7, 5, 12, false},
		{12, 0, 12, false},
		{0, 0, 0, false},
		{math.MaxInt64, 0, 12, false},
		{math.MaxInt64, 0, math.MaxInt64, false},
		{math.MaxInt64 - 1, 0, math.MaxInt64, true},
	} {
		if got := hasMore(c.offset, c.returned, c.total); got != c.want {
			t.Errorf("hasMore(%d, %d, %d) = %v", c.offset, c.returned, c.total, got)
		}
	}
}
