package documents

import (
	"math/big"
	"testing"
)

// `rows[:page_size]` for every kind of end a request can name, held to what
// Python's own slice gives: the table below was printed by CPython
// (`len(list(range(n))[:end])`), not written by hand.
//
// Only some of it is reachable through the route -- a negative page size
// other than -1 is refused by Postgres before there are rows to slice -- so
// the replayed tables cannot hold the function to all of it, and this does.
func TestTheSliceEndIsPythons(t *testing.T) {
	for _, c := range []struct {
		length int
		end    string
		want   int
	}{
		{0, "-30", 0},
		{0, "-26", 0},
		{0, "-25", 0},
		{0, "-4", 0},
		{0, "-3", 0},
		{0, "-1", 0},
		{0, "0", 0},
		{0, "1", 0},
		{0, "2", 0},
		{0, "3", 0},
		{0, "24", 0},
		{0, "25", 0},
		{0, "26", 0},
		{0, "1000000000000000000000000000000", 0},
		{0, "-1000000000000000000000000000000", 0},
		{1, "-30", 0},
		{1, "-26", 0},
		{1, "-25", 0},
		{1, "-4", 0},
		{1, "-3", 0},
		{1, "-1", 0},
		{1, "0", 0},
		{1, "1", 1},
		{1, "2", 1},
		{1, "3", 1},
		{1, "24", 1},
		{1, "25", 1},
		{1, "26", 1},
		{1, "1000000000000000000000000000000", 1},
		{1, "-1000000000000000000000000000000", 0},
		{3, "-30", 0},
		{3, "-26", 0},
		{3, "-25", 0},
		{3, "-4", 0},
		{3, "-3", 0},
		{3, "-1", 2},
		{3, "0", 0},
		{3, "1", 1},
		{3, "2", 2},
		{3, "3", 3},
		{3, "24", 3},
		{3, "25", 3},
		{3, "26", 3},
		{3, "1000000000000000000000000000000", 3},
		{3, "-1000000000000000000000000000000", 0},
		{25, "-30", 0},
		{25, "-26", 0},
		{25, "-25", 0},
		{25, "-4", 21},
		{25, "-3", 22},
		{25, "-1", 24},
		{25, "0", 0},
		{25, "1", 1},
		{25, "2", 2},
		{25, "3", 3},
		{25, "24", 24},
		{25, "25", 25},
		{25, "26", 25},
		{25, "1000000000000000000000000000000", 25},
		{25, "-1000000000000000000000000000000", 0},
	} {
		end, readable := new(big.Int).SetString(c.end, 10)
		if !readable {
			t.Fatalf("%q is not an integer", c.end)
		}
		if got := pythonSliceEnd(c.length, end); got != c.want {
			t.Errorf("a list of %d sliced to [:%s] ends at %d, and Python ends it at %d",
				c.length, c.end, got, c.want)
		}
	}
}
