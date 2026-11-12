package publicchat

import (
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"
)

// TestPythonsOwnSetOrder holds the mirror of CPython's set to CPython.
//
// testdata/python_sets.json is the order the interpreter the Python version
// ran on gave back each of its sets in, recorded before it was retired. Every
// set is rebuilt here by the same adds in the same order, and the whole of
// its iteration order has to agree -- not only its first member, which is
// the one thing the route reads, because an order that is right at the front
// by luck is wrong at the front for some other set.
func TestPythonsOwnSetOrder(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "python_sets.json"))
	if err != nil {
		t.Fatalf("reading CPython's set orders: %v", err)
	}
	var table struct {
		Python string `json:"python"`
		Cases  []struct {
			Added   []any `json:"added"`
			Ordered []any `json:"ordered"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatalf("parsing CPython's set orders: %v", err)
	}
	if len(table.Cases) == 0 {
		t.Fatal("the table is empty, so it proves nothing")
	}

	resized, withBools := 0, 0
	for index, c := range table.Cases {
		built := &intSet{}
		for _, added := range c.Added {
			value, isBool := setMember(t, added)
			built.Add(value, isBool)
		}
		ordered := built.Ordered()
		if len(ordered) != len(c.Ordered) {
			t.Errorf("set %d: %d members here and %d in CPython", index, len(ordered), len(c.Ordered))
			continue
		}
		for position, want := range c.Ordered {
			value, isBool := setMember(t, want)
			got := ordered[position]
			if got.isBool != isBool || got.value.Cmp(value) != 0 {
				t.Errorf("set %d: member %d is %v (bool: %v) here and %v in CPython",
					index, position, got.value, got.isBool, want)
				break
			}
			if isBool {
				withBools++
			}
		}
		if len(built.slots) > setMinimumSize {
			resized++
		}
	}
	// The two things the mirror could be wrong about without a short set
	// noticing: growing the table, and a bool keeping its own spelling.
	if resized == 0 || withBools == 0 {
		t.Errorf("the table exercised %d resized sets and %d bool members; it needs both", resized, withBools)
	}
	t.Logf("%d sets ordered as CPython %s orders them (%d grew past the first table)",
		len(table.Cases), table.Python, resized)
}

// setMember reads one member as the script wrote it: a JSON bool for a bool,
// and text for an integer of any width.
func setMember(t *testing.T, written any) (*big.Int, bool) {
	t.Helper()
	switch held := written.(type) {
	case bool:
		if held {
			return big.NewInt(1), true
		}
		return big.NewInt(0), true
	case string:
		value, ok := new(big.Int).SetString(held, 10)
		if !ok {
			t.Fatalf("%q is not an integer", held)
		}
		return value, false
	}
	t.Fatalf("a set member that is neither a bool nor text: %v", written)
	return nil, false
}
