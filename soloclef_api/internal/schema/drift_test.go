package schema

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// moduleRoot is where sqlc.yaml is, two levels above this package.
const moduleRoot = "../.."

func TestTheCheckedInSchemaIsWhatTheMigrationsProduce(t *testing.T) {
	if os.Getenv(DriftCheckVariable) == "" {
		t.Skipf("set %s=1 to run this: it starts a throwaway Postgres, migrates it "+
			"with the backend image and dumps it, which takes about fifteen seconds "+
			"and needs Docker", DriftCheckVariable)
	}

	want, err := os.ReadFile(Definition)
	if err != nil {
		t.Fatalf("reading the checked-in schema: %v", err)
	}

	script, err := filepath.Abs(DumpScript)
	if err != nil {
		t.Fatalf("locating %s: %v", DumpScript, err)
	}
	var produced, reasons bytes.Buffer
	dump := exec.Command(script)
	dump.Stdout = &produced
	dump.Stderr = &reasons
	if err := dump.Run(); err != nil {
		t.Fatalf("%s failed: %v\n%s", DumpScript, err, reasons.String())
	}

	if produced.String() == string(want) {
		return
	}
	t.Errorf("%s is no longer the schema the migrations produce.\n\n%s\n\n"+
		"A migration has changed the database under this tier. Read the diff "+
		"against soloclef_api/internal — a renamed or dropped column that a "+
		"query names is a live route answering 500 — then regenerate:\n"+
		"    internal/schema/%s > internal/schema/%s && sqlc generate",
		Definition, firstDifferingLines(string(want), produced.String()), DumpScript, Definition)
}

func TestTheGeneratedQueriesMatchTheCheckedInSchema(t *testing.T) {
	sqlc, err := exec.LookPath("sqlc")
	if err != nil {
		t.Skip("sqlc is not on PATH; install it to check the generated code is current")
	}

	diff := exec.Command(sqlc, "diff")
	diff.Dir = moduleRoot
	output, err := diff.CombinedOutput()
	if err != nil {
		t.Errorf("the committed generated code is not what %s and the queries "+
			"produce. Run `sqlc generate`.\n%s", Definition, output)
	}
}

func TestTheCheckedInSchemaCarriesNothingThatChangesBetweenDumps(t *testing.T) {
	// pg_dump brackets its output with \restrict and \unrestrict lines carrying
	// a fresh random nonce, and names the server's patch version in a comment.
	// The dump script drops all three; a schema.sql pasted in by hand would
	// keep them and make the drift check fail on every run, which is a check
	// nobody then believes.
	schema, err := os.ReadFile(Definition)
	if err != nil {
		t.Fatalf("reading the checked-in schema: %v", err)
	}
	for _, unwanted := range []string{`\restrict `, `\unrestrict `, "-- Dumped from database version ", "-- Dumped by pg_dump version "} {
		if bytes.Contains(schema, []byte(unwanted)) {
			t.Errorf("%s still carries %q; produce it with %s", Definition, unwanted, DumpScript)
		}
	}
}

// firstDifferingLines quotes the first line that differs and its neighbours,
// because the schema is thousands of lines long.
func firstDifferingLines(want, got string) string {
	wantLines, gotLines := strings.Split(want, "\n"), strings.Split(got, "\n")
	at := 0
	for at < len(wantLines) && at < len(gotLines) && wantLines[at] == gotLines[at] {
		at++
	}
	quote := func(label string, lines []string) string {
		if at >= len(lines) {
			return label + " ends here"
		}
		end := min(at+3, len(lines))
		return label + " line " + strconv.Itoa(at+1) + ":\n  " + strings.Join(lines[at:end], "\n  ")
	}
	return quote("committed", wantLines) + "\n" + quote("produced", gotLines)
}
