package appversion

import (
	"errors"
	"testing"
)

func mustParse(t *testing.T, text string) Version {
	t.Helper()
	v, err := Parse(text)
	if err != nil {
		t.Fatalf("Parse(%q): %v", text, err)
	}
	return v
}

// Numbers, part by part: "1.10.0" is after "1.9.0", which a text comparison
// gets backwards, and a missing part is a zero.
func TestVersionsCompareAsNumbersPartByPart(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.10.0", "1.9.0", 1},
		{"1.9.0", "1.10.0", -1},
		{"1.2.0", "1.2.0", 0},
		{"1.0", "1.0.0", 0},
		{"1", "1.0.0.0", 0},
		{"1.0.1", "1.0", 1},
		{"2.0", "1.99.99", 1},
		{"0.9", "1", -1},
		{"1.2.3.4", "1.2.3.5", -1},
		{"010.0", "9.0", 1},
	}
	for _, c := range cases {
		if got := mustParse(t, c.a).Compare(mustParse(t, c.b)); got != c.want {
			t.Errorf("%s vs %s = %d, want %d", c.a, c.b, got, c.want)
		}
		if older := mustParse(t, c.a).Older(mustParse(t, c.b)); older != (c.want < 0) {
			t.Errorf("%s older than %s = %v", c.a, c.b, older)
		}
	}
}

// A version is kept as it was written, so "1.0" is stored and shown as Apple
// wrote it.
func TestAVersionKeepsItsSpelling(t *testing.T) {
	if s := mustParse(t, "1.0").String(); s != "1.0" {
		t.Errorf("String = %q", s)
	}
}

// Anything that is not a plain dotted number is refused, never read as the
// nearest version: a pre-release tag, a sign, spaces, an empty part, too
// many parts, a part too long to be a release.
func TestWhatIsNotAPlainVersionIsRefused(t *testing.T) {
	for _, bad := range []string{
		"", ".", "1.", ".1", "1..2", "1.2.0-beta", "v1.2", "+1", "-1", " 1.2", "1.2 ",
		"1.2.3.4.5", "1234567890", "1.a", "١.٢", "1,2",
	} {
		if _, err := Parse(bad); !errors.Is(err, ErrShape) {
			t.Errorf("Parse(%q) = %v, want ErrShape", bad, err)
		}
	}
	if _, err := Parse("999999999.0.0.1"); err != nil {
		t.Errorf("the longest allowed part was refused: %v", err)
	}
}
