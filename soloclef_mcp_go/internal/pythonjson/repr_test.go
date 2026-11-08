package pythonjson_test

import (
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/pythonjson"
)

// The expected strings below came out of CPython's repr() and out of the running
// Python MCP server's refusals, not out of documentation.
func TestReprMatchesPython(t *testing.T) {
	cases := []struct{ source, want string }{
		{`null`, "None"},
		{`true`, "True"},
		{`false`, "False"},
		{`0`, "0"},
		{`-7`, "-7"},
		{`2.5`, "2.5"},
		{`3.0`, "3.0"},
		{`-0.5`, "-0.5"},
		{`1e3`, "1000.0"},
		{`1e17`, "1e+17"},
		{`1e-7`, "1e-07"},
		{`""`, `''`},
		{`"plain"`, `'plain'`},
		{`"it's"`, `"it's"`},
		{`"it's \"both\""`, `'it\'s "both"'`},
		{`"back\\slash"`, `'back\\slash'`},
		{`"a\nb\tc\rd"`, `'a\nb\tc\rd'`},
		{`"\u0001"`, `'\x01'`},
		{`"café ☕"`, `'café ☕'`},
		{`[]`, "[]"},
		{`[1,"a",null]`, `[1, 'a', None]`},
		{`{}`, "{}"},
		{`{"b":1,"a":2}`, `{'b': 1, 'a': 2}`},
		{`{"a":{"b":[true]}}`, `{'a': {'b': [True]}}`},
	}
	for _, each := range cases {
		value, err := pythonjson.DecodeAny([]byte(each.source))
		if err != nil {
			t.Fatalf("%s: %v", each.source, err)
		}
		if got := pythonjson.Of(value); got != each.want {
			t.Errorf("repr(%s) is %s, want %s", each.source, got, each.want)
		}
	}
}

func TestTypeNameMatchesPython(t *testing.T) {
	cases := map[string]string{
		`null`: "NoneType", `true`: "bool", `7`: "int", `7.5`: "float",
		`"a"`: "str", `[]`: "list", `{}`: "dict",
	}
	for source, want := range cases {
		value, err := pythonjson.DecodeAny([]byte(source))
		if err != nil {
			t.Fatal(err)
		}
		if got := pythonjson.TypeName(value); got != want {
			t.Errorf("%s is %s, want %s", source, got, want)
		}
	}
}

// pydantic cuts a value's repr at 50 characters, keeping the first 25 and the
// last 24 with an ellipsis between. The boundary was measured against the
// running server: a repr of exactly 50 is left alone and one of 51 is cut.
func TestTruncationMatchesPydantic(t *testing.T) {
	cases := []struct{ source, want string }{
		{`{"query":"` + repeat("x", 37) + `"}`,
			`{'query': '` + repeat("x", 37) + `'}`},
		{`{"query":"` + repeat("x", 38) + `"}`,
			`{'query': '` + repeat("x", 14) + `...` + repeat("x", 22) + `'}`},
		{`{"query":"` + repeat("x", 400) + `"}`,
			`{'query': '` + repeat("x", 14) + `...` + repeat("x", 22) + `'}`},
	}
	for _, each := range cases {
		value, err := pythonjson.DecodeAny([]byte(each.source))
		if err != nil {
			t.Fatal(err)
		}
		got := pythonjson.Truncated(value)
		if got != each.want {
			t.Errorf("truncated repr is\n %s\nwant\n %s", got, each.want)
		}
		if len(got) > 52 {
			t.Errorf("truncated repr is %d characters, pydantic's cap is 52", len(got))
		}
	}
}

// A multi-byte character at the cut is dropped rather than halved, so the
// refusal is still valid UTF-8 and a client can read it.
func TestTruncationDoesNotSplitACharacter(t *testing.T) {
	value, err := pythonjson.DecodeAny([]byte(`{"query":"` + repeat("☕", 40) + `"}`))
	if err != nil {
		t.Fatal(err)
	}
	got := pythonjson.Truncated(value)
	for index, char := range got {
		if char == '�' {
			t.Fatalf("byte %d of the truncated repr is not a whole character: %q",
				index, got)
		}
	}
}

func repeat(text string, times int) string {
	out := ""
	for range times {
		out += text
	}
	return out
}
