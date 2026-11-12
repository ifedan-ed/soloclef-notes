package schemaversion

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestCompareSaysWhichWayADatabaseDiffers(t *testing.T) {
	cases := []struct {
		name     string
		expected []int64
		applied  []int64
		want     []string // substrings of the refusal; none means accepted
	}{
		{name: "the same migrations", expected: []int64{1, 2}, applied: []int64{1, 2}},
		{name: "a database with nothing applied", expected: []int64{1}, applied: nil,
			want: []string{"behind", "at version 0", "expects version 1", "not applied: 1", MigrateCommand}},
		{name: "a database one migration behind", expected: []int64{1, 2, 3}, applied: []int64{1, 2},
			want: []string{"behind", "at version 2", "expects version 3", "not applied: 3"}},
		{name: "a hole below the newest", expected: []int64{1, 2, 3}, applied: []int64{1, 3},
			want: []string{"behind", "not applied: 2"}},
		{name: "a database a newer build migrated", expected: []int64{1, 2}, applied: []int64{1, 2, 3},
			want: []string{"does not know (3)", "expects version 2", "older than the database"}},
		{name: "both at once is reported as the one a migration cannot fix", expected: []int64{1, 2}, applied: []int64{1, 7},
			want: []string{"does not know (7)"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := compare(c.expected, c.applied)
			if len(c.want) == 0 {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("accepted")
			}
			for _, want := range c.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal %q does not say %q", err, want)
				}
			}
		})
	}
}

func TestVersionsAreReadFromTheFileNames(t *testing.T) {
	file := &fstest.MapFile{}
	got, err := versionsIn(fstest.MapFS{"00002_second.sql": file, "00001_baseline.sql": file, "embed.go": file})
	if err != nil || len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("versions = %v, %v", got, err)
	}
	for name, files := range map[string]fstest.MapFS{
		"no migrations":     {},
		"no number":         {"baseline.sql": file},
		"no name":           {"00001.sql": file},
		"version zero":      {"00000_nothing.sql": file},
		"one number, twice": {"00001_a.sql": file, "1_b.sql": file},
	} {
		if _, err := versionsIn(files); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// The migrations compiled into the test binary are the ones every service
// will expect, so a badly named file fails here and not at somebody's
// start-up.
func TestTheEmbeddedMigrationsAreNumbered(t *testing.T) {
	versions, err := Expected()
	if err != nil {
		t.Fatal(err)
	}
	if versions[0] != 1 {
		t.Errorf("the first migration is %d, not the baseline", versions[0])
	}
	latest, err := Latest()
	if err != nil || latest != versions[len(versions)-1] {
		t.Errorf("Latest = %d, %v", latest, err)
	}
}
