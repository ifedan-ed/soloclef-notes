package pythonjson

import "testing"

// The want strings below were read out of the `log_metadata` column after the
// Python tier wrote them, not written from SQLAlchemy's documentation.
func TestMarshalColumnWritesWhatSQLAlchemyStored(t *testing.T) {
	cases := []struct {
		name string
		body []byte
		want string
	}{
		{
			name: "the shape POST /api/v1/logs was asked for",
			body: []byte(`{"b":1,"a":[1,2.5,null,true],"n":{"deep":"x"},"u":"< & \u2028 \u00e9"}`),
			want: `{"b": 1, "a": [1, 2.5, null, true], "n": {"deep": "x"}, "u": "< & \u2028 \u00e9"}`,
		},
		{name: "an empty object", body: []byte(`{}`), want: `{}`},
		{name: "an empty array", body: []byte(`[]`), want: `[]`},
		{
			name: "an astral character becomes a surrogate pair",
			body: []byte(`{"k":"\ud83d\ude00"}`),
			want: `{"k": "\ud83d\ude00"}`,
		},
		{
			name: "a control character and the short escapes",
			body: []byte(`{"k":"\u0001\t\n"}`),
			want: `{"k": "\u0001\t\n"}`,
		},
		{
			name: "an integer wider than int64 keeps its text",
			body: []byte(`{"k":99999999999999999999}`),
			want: `{"k": 99999999999999999999}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value, err := Decode(tc.body)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			stored, err := MarshalColumn(value)
			if err != nil {
				t.Fatalf("MarshalColumn: %v", err)
			}
			if string(stored) != tc.want {
				t.Errorf("stored\n got %s\nwant %s", stored, tc.want)
			}
		})
	}
}

// The response is rendered by the other encoder, and the two must not be
// confused: one carries the spaces and the escapes and the other does not.
func TestMarshalIsStillTheCompactNonASCIIForm(t *testing.T) {
	value, err := Decode([]byte(`{"a":1,"u":"é"}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	rendered, err := Marshal(value)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(rendered) != `{"a":1,"u":"é"}` {
		t.Errorf("got %s", rendered)
	}
}
