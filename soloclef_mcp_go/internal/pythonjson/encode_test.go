package pythonjson_test

import (
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/pythonjson"
)

// Every expected block below is what
// json.dumps(value, indent=2, ensure_ascii=False, default=str) printed, which is
// what every tool's JSON form returns. Go's own encoder sorts members, escapes
// <, > and &, and prints 1e17 as 1e+17 only by accident of format, so none of it
// can be left to the standard library.
func TestIndentedMatchesPythonJSONDumps(t *testing.T) {
	cases := []struct{ source, want string }{
		{`{}`, "{}"},
		{`[]`, "[]"},
		{`{"b":1,"a":2}`, "{\n  \"b\": 1,\n  \"a\": 2\n}"},
		{`{"items":[{"x":1.5,"y":3.0}]}`,
			"{\n  \"items\": [\n    {\n      \"x\": 1.5,\n      \"y\": 3.0\n    }\n  ]\n}"},
		{`[1,[2,[3]]]`, "[\n  1,\n  [\n    2,\n    [\n      3\n    ]\n  ]\n]"},
		{`{"t":"a<b>&c"}`, "{\n  \"t\": \"a<b>&c\"\n}"},
		{`{"u":"café ☕"}`, "{\n  \"u\": \"café ☕\"\n}"},
		{`{"n":null,"f":false}`, "{\n  \"n\": null,\n  \"f\": false\n}"},
		{`{"s":"a\nb"}`, "{\n  \"s\": \"a\\nb\"\n}"},
		{`{"big":1e17}`, "{\n  \"big\": 1e+17\n}"},
		{`{"path":"a/b"}`, "{\n  \"path\": \"a/b\"\n}"},
	}
	for _, each := range cases {
		value, err := pythonjson.DecodeAny([]byte(each.source))
		if err != nil {
			t.Fatalf("%s: %v", each.source, err)
		}
		if got := pythonjson.Indented(value, 2); got != each.want {
			t.Errorf("%s encoded as\n%s\nwant\n%s", each.source, got, each.want)
		}
	}
}

// Member order survives a decode and re-encode. Go's maps would sort it, and a
// tool result is read by a model that was shown the backend's own order.
func TestMemberOrderSurvivesTheRoundTrip(t *testing.T) {
	const source = `{"zebra":1,"apple":2,"mango":3}`
	value, err := pythonjson.DecodeAny([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	if got := pythonjson.Compact(value); got != source {
		t.Errorf("compact re-encoding is %s, want %s", got, source)
	}
}

// A whole number written with a fraction stays a float, because a refusal tells
// 3 from 3.0 and so does json.dumps.
func TestNumberSpellingSurvivesTheRoundTrip(t *testing.T) {
	for source, want := range map[string]string{
		"3": "3", "3.0": "3.0", "-0.0": "-0.0", "1e2": "100.0",
	} {
		value, err := pythonjson.DecodeAny([]byte(source))
		if err != nil {
			t.Fatal(err)
		}
		if got := pythonjson.Indented(value, 2); got != want {
			t.Errorf("%s encoded as %s, want %s", source, got, want)
		}
	}
}

func TestWithoutNullsDropsOnlyNullMembers(t *testing.T) {
	value, err := pythonjson.DecodeAny([]byte(`{"a":1,"b":null,"c":"","d":[],"e":false}`))
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"a":1,"c":"","d":[],"e":false}`
	if got := pythonjson.Compact(pythonjson.WithoutNulls(value)); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestEmptyAndNullArgumentsDecodeToAnEmptyObject(t *testing.T) {
	for _, source := range []string{"", "   ", "null"} {
		value, err := pythonjson.DecodeObject([]byte(source))
		if err != nil {
			t.Fatalf("%q: %v", source, err)
		}
		if value.Kind != pythonjson.Object || len(value.Members) != 0 {
			t.Errorf("%q decoded to %+v, want an empty object", source, value)
		}
	}
}

func TestANonObjectArgumentsPayloadIsRefused(t *testing.T) {
	if _, err := pythonjson.DecodeObject([]byte(`[1,2]`)); err == nil {
		t.Error("an array of arguments should be refused")
	}
}

func TestTruthyFollowsPython(t *testing.T) {
	cases := map[string]bool{
		`null`: false, `false`: false, `true`: true, `0`: false, `1`: true,
		`0.0`: false, `""`: false, `"a"`: true, `[]`: false, `[0]`: true,
		`{}`: false, `{"a":1}`: true,
	}
	for source, want := range cases {
		value, err := pythonjson.DecodeAny([]byte(source))
		if err != nil {
			t.Fatal(err)
		}
		if got := pythonjson.Truthy(value); got != want {
			t.Errorf("%s is %v, want %v", source, got, want)
		}
	}
}

// A renderer reads a reply with dict.get and an f-string, so a null member
// prints "None" and a number prints itself. Reading more carefully than that
// would be a different result from the Python server's.
func TestTextReadsAMemberAsPythonPrintsIt(t *testing.T) {
	value, err := pythonjson.DecodeAny(
		[]byte(`{"title":null,"id":7,"score":0.5,"tags":["a"],"name":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"title": "None", "id": "7", "score": "0.5", "tags": "['a']", "name": "x",
	}
	for key, want := range cases {
		if got := pythonjson.Text(value, key, "FALLBACK"); got != want {
			t.Errorf("%s reads as %q, want %q", key, got, want)
		}
	}
	if got := pythonjson.Text(value, "absent", "FALLBACK"); got != "FALLBACK" {
		t.Errorf("an absent member reads as %q, want the fallback", got)
	}
}
