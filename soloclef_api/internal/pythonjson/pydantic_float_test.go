package pythonjson

import (
	"math"
	"testing"
)

// Every pair below came out of the running Python tier through
// POST /api/v1/logs' own log_metadata, which is serialised by pydantic-core,
// beside the repr form the same value is *stored* as. The two columns are the
// whole reason this type exists.
func TestPydanticFloatIsNotPythonsRepr(t *testing.T) {
	cases := []struct {
		value    float64
		pydantic string
		repr     string
	}{
		{1e15, "1000000000000000.0", "1000000000000000.0"},
		{1e16, "1e16", "1e+16"},
		{1e17, "1e17", "1e+17"},
		{1e21, "1e21", "1e+21"},
		{1e100, "1e100", "1e+100"},
		{1e-4, "0.0001", "0.0001"},
		{1e-5, "0.00001", "1e-05"},
		{1e-6, "1e-6", "1e-06"},
		{1e-7, "1e-7", "1e-07"},
		{1.5e-10, "1.5e-10", "1.5e-10"},
		{5e-324, "5e-324", "5e-324"},
		{1e-323, "1e-323", "1e-323"},
		{2.2250738585072014e-308, "2.2250738585072014e-308", "2.2250738585072014e-308"},
		{1.7976931348623157e308, "1.7976931348623157e308", "1.7976931348623157e+308"},
		{0.1, "0.1", "0.1"},
		{2.5, "2.5", "2.5"},
		{100.0, "100.0", "100.0"},
		{1000.0, "1000.0", "1000.0"},
		{0.0, "0.0", "0.0"},
		// A Go literal -0.0 folds to 0.0, so the sign is made explicitly.
		{math.Copysign(0, -1), "-0.0", "-0.0"},
		{3.141592653589793, "3.141592653589793", "3.141592653589793"},
		{1234567890123456.0, "1234567890123456.0", "1234567890123456.0"},
		{12345678901234567.0, "1.2345678901234568e16", "1.2345678901234568e+16"},
		{-1e16, "-1e16", "-1e+16"},
		{-1e-5, "-0.00001", "-1e-05"},
		{0.30000000000000004, "0.30000000000000004", "0.30000000000000004"},
		{123456789012345678901234567890.0, "1.2345678901234568e29", "1.2345678901234568e+29"},
	}
	for _, tc := range cases {
		rendered, err := Marshal(PydanticFloat(tc.value))
		if err != nil {
			t.Errorf("%v: %v", tc.value, err)
			continue
		}
		if string(rendered) != tc.pydantic {
			t.Errorf("pydantic form of %v: got %s, want %s", tc.value, rendered, tc.pydantic)
		}
		stored, err := Marshal(tc.value)
		if err != nil {
			t.Errorf("%v: %v", tc.value, err)
			continue
		}
		if string(stored) != tc.repr {
			t.Errorf("repr form of %v: got %s, want %s", tc.value, stored, tc.repr)
		}
	}
}

// Negative zero keeps its sign in both forms, which is why it is not written
// through a magnitude test.
func TestPydanticValuesConvertsEveryFloatAndNothingElse(t *testing.T) {
	value, err := Decode([]byte(`{"f":1e16,"i":99999999999999999999,"s":"x","n":null,"a":[1e-5,{"g":2.5}]}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	rendered, err := Marshal(PydanticValues(value))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"f":1e16,"i":99999999999999999999,"s":"x","n":null,"a":[0.00001,{"g":2.5}]}`
	if string(rendered) != want {
		t.Errorf("got  %s\nwant %s", rendered, want)
	}
}
