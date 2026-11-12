package chat

import (
	"io/fs"
	"strings"
	"testing"

	agentpostgres "git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint/postgres"
	"git.danvics.com/danvics/soloclef/soloclef_schema/migrations"
)

// The checkpoint table is the agent library's: it exports the statements
// that create it, for an application whose migrations own the database.
// This one's do, so the statements are written again in a migration, and a
// second copy is a copy that can drift. This fails when the library changes
// its table and no migration has followed.
func TestTheMigrationsCreateTheAgentLibrarysCheckpointTable(t *testing.T) {
	// One spelling of a statement: no "IF NOT EXISTS" (a migration runs
	// once), and every run of white space as one space.
	normal := func(statement string) string {
		return strings.Join(strings.Fields(strings.ReplaceAll(statement, "IF NOT EXISTS ", "")), " ")
	}
	names, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil || len(names) == 0 {
		t.Fatalf("migrations = %v, %v", names, err)
	}
	var all strings.Builder
	for _, name := range names {
		content, err := fs.ReadFile(migrations.Files, name)
		if err != nil {
			t.Fatal(err)
		}
		// Comments sit between the statement's lines in a migration and
		// are not part of it.
		for _, line := range strings.Split(string(content), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "--") {
				all.WriteString(line + "\n")
			}
		}
	}
	schema := normal(all.String())
	for what, statement := range map[string]string{"table": agentpostgres.Schema, "index": agentpostgres.TreeIndex} {
		if !strings.Contains(schema, normal(statement)) {
			t.Errorf("no migration creates the checkpoint %s as soloclef_agent declares it:\n%s", what, statement)
		}
	}
}
