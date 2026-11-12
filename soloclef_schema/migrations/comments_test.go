package migrations

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// Every table says, in the SQL that creates it, why it exists. The file is
// the only description of the schema there is, and sqlc and a reviewer both
// read it, so a table added without its reason fails here.
func TestEveryTableSaysWhyItExists(t *testing.T) {
	names, err := fs.Glob(Files, "*.sql")
	if err != nil || len(names) == 0 {
		t.Fatalf("migrations = %v, %v", names, err)
	}
	create := regexp.MustCompile(`^CREATE TABLE\s+(\S+)`)
	for _, name := range names {
		content, err := fs.ReadFile(Files, name)
		if err != nil {
			t.Fatal(err)
		}
		// A goose file has both sections, or it cannot be rolled back while
		// it is being written.
		for _, section := range []string{"-- +goose Up", "-- +goose Down"} {
			if !strings.Contains(string(content), section) {
				t.Errorf("%s has no %q section", name, section)
			}
		}
		lines := strings.Split(string(content), "\n")
		for index, line := range lines {
			match := create.FindStringSubmatch(line)
			if match == nil {
				continue
			}
			if index == 0 || !strings.HasPrefix(lines[index-1], "-- ") {
				t.Errorf("%s: table %s has no comment above it saying why it exists", name, match[1])
			}
		}
	}
}
