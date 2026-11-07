package pythonjson

import (
	"math"
	"testing"
)

// Every want below is the output of CPython's
// json.dumps(value, ensure_ascii=False, allow_nan=False, separators=(",", ":")),
// which is what Starlette's JSONResponse calls.

func TestMarshalKeepsInsertionOrder(t *testing.T) {
	inner := NewObject()
	inner.Set("b", 2)
	inner.Set("a", 3)
	outer := NewObject()
	outer.Set("total", 1)
	outer.Set("by", inner)
	outer.Set("items", []any{1, "x", nil, true})

	got, err := Marshal(outer)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	const want = `{"total":1,"by":{"b":2,"a":3},"items":[1,"x",null,true]}`
	if string(got) != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}

func TestMarshalEscapesOnlyWhatPythonEscapes(t *testing.T) {
	value := "a<b>c&d \"q\" \\ back   line\nnon-ascii: ‘Stand’ \x01\x08"

	got, err := Marshal(value)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	const want = "\"a<b>c&d \\\"q\\\" \\\\ back   line\\nnon-ascii: ‘Stand’ \\u0001\\b\""
	if string(got) != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}

func TestMarshalFormatsNumbersAsPythonReprDoes(t *testing.T) {
	got, err := Marshal([]any{
		1000000.0, 0.1, 1e16, 1e-5, math.Copysign(0, -1), RawInt("123456789012345678901234567890"), 1.5, 2.0,
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	const want = `[1000000.0,0.1,1e+16,1e-05,-0.0,123456789012345678901234567890,1.5,2.0]`
	if string(got) != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}

func TestMarshalRefusesValuesPythonWouldNotEmit(t *testing.T) {
	for name, value := range map[string]any{
		"not a number": math.Inf(1),
		"unsupported":  struct{ A int }{A: 1},
	} {
		if _, err := Marshal([]any{value}); err == nil {
			t.Errorf("%s: want an error, got none", name)
		}
	}
}
