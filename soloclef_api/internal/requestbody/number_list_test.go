package requestbody

import (
	"strconv"
	"strings"
	"testing"
)

func modelOf(t *testing.T, body string) *Model {
	t.Helper()
	object, failure, err := Decode("application/json", []byte(body))
	if err != nil || failure != nil {
		t.Fatalf("%s: the body did not decode (%v, %v)", body, failure, err)
	}
	return NewModel(object)
}

// Every vector here came out of the Python tier: the same JSON body was posted
// to POST /api/v1/messages/comments/batch and the answer read off. The
// surprising two are the last: 201 items with a bad item among them is
// answered with the *length* failure and not the item's, and an id no column
// can hold is not a refusal at all.
func TestAListOfIntegersIsReadAsPydanticReadsOne(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		want       []int64
		refusals   []string
		locations  [][]string
		outOfRange bool
	}{
		{
			name: "every lax spelling an item can have",
			body: `{"message_ids":[1,"1","1 ","1_0",1.0,true,false]}`,
			want: []int64{1, 1, 1, 10, 1, 1, 0},
		},
		{
			name:      "an empty list is the minimum's refusal",
			body:      `{"message_ids":[]}`,
			refusals:  []string{"List should have at least 1 item after validation, not 0"},
			locations: [][]string{{"body", "message_ids"}},
		},
		{
			name:      "a value that is not a list",
			body:      `{"message_ids":"x"}`,
			refusals:  []string{NotAList},
			locations: [][]string{{"body", "message_ids"}},
		},
		{
			name:      "null is not a list either",
			body:      `{"message_ids":null}`,
			refusals:  []string{NotAList},
			locations: [][]string{{"body", "message_ids"}},
		},
		{
			name:      "an absent field",
			body:      `{}`,
			refusals:  []string{FieldRequired},
			locations: [][]string{{"body", "message_ids"}},
		},
		{
			name: "each bad item under its own index, as text",
			body: `{"message_ids":[1,"abc",2.5,true,null]}`,
			refusals: []string{
				IntegerUnreadable,
				IntegerFractional,
				NotAnInteger,
			},
			locations: [][]string{
				{"body", "message_ids", "1"},
				{"body", "message_ids", "2"},
				{"body", "message_ids", "4"},
			},
		},
		{
			name:       "an id wider than the column is held, not refused",
			body:       `{"message_ids":[99999999999999999999]}`,
			want:       []int64{0},
			outOfRange: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := modelOf(t, tc.body)
			got := model.NumberList("message_ids", 1, 200)

			var refusals []string
			var locations [][]string
			for _, failure := range model.Failures() {
				refusals = append(refusals, failure.Message)
				locations = append(locations, failure.Location)
			}
			if strings.Join(refusals, "|") != strings.Join(tc.refusals, "|") {
				t.Fatalf("refusals = %q, want %q", refusals, tc.refusals)
			}
			for index, wanted := range tc.locations {
				if strings.Join(locations[index], "/") != strings.Join(wanted, "/") {
					t.Errorf("location %d = %q, want %q", index, locations[index], wanted)
				}
			}
			if model.OutOfRange() != tc.outOfRange {
				t.Errorf("out of range = %v, want %v", model.OutOfRange(), tc.outOfRange)
			}
			if len(tc.refusals) > 0 {
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("read %v, want %v", got, tc.want)
			}
			for index, wanted := range tc.want {
				if got[index] != wanted {
					t.Errorf("item %d = %d, want %d", index, got[index], wanted)
				}
			}
		})
	}
}

// The maximum is checked against the length the request sent, before any item
// is read: 201 items with an unreadable one among them is answered with the
// length failure alone. Asked of the running tier both ways.
func TestTheMaximumLengthIsCheckedBeforeTheItemsAre(t *testing.T) {
	numbers := make([]string, 0, 201)
	numbers = append(numbers, `"x"`)
	for id := 2; id <= 201; id++ {
		numbers = append(numbers, strconv.Itoa(id))
	}
	model := modelOf(t, `{"message_ids":[`+strings.Join(numbers, ",")+`]}`)
	model.NumberList("message_ids", 1, 200)

	failures := model.Failures()
	if len(failures) != 1 {
		t.Fatalf("got %d failures, want the length's alone: %+v", len(failures), failures)
	}
	want := "List should have at most 200 items after validation, not 201"
	if failures[0].Message != want {
		t.Errorf("message = %q, want %q", failures[0].Message, want)
	}
}
