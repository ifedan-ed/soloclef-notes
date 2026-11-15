package installedbuilds

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// moduleRoot is soloclef_api, where Answer.Code is relative to.
const moduleRoot = "../.."

var markerLine = regexp.MustCompile(`installed-builds: ([a-z0-9-]+)`)

// Each compatibility answer is needed by at least one installed build. When
// a snapshot is retired, this names what its builds alone needed and where
// it is, so it is deleted rather than kept for nobody.
func TestEveryCompatibilityAnswerIsNeededByAnInstalledBuild(t *testing.T) {
	builds := mustLoad(t)
	for _, answer := range Answers {
		if needed := answer.NeededBy(builds); len(needed) == 0 {
			var needs []string
			for _, need := range answer.Needs {
				needs = append(needs, need.String())
			}
			t.Errorf("no installed build needs %q any more (it serves %s): delete it from %s, "+
				"every line marked %q, and its entry in compat.go",
				answer.ID, strings.Join(needs, ", "), strings.Join(answer.Code, ", "), Marker(answer.ID))
		}
	}
}

// Each retirement is a call some installed build still makes: once none
// does, the entry goes.
func TestEveryRetirementIsACallABuildMakes(t *testing.T) {
	builds := mustLoad(t)
	for _, retired := range Retired {
		if retired.Status != 404 && retired.Status != 405 && retired.Status != 401 {
			t.Errorf("retirement %s answers %d; a call no route serves gets the router's 404 or 405, or the gate's 401", retired.Operation, retired.Status)
		}
		if retired.Why == "" {
			t.Errorf("retirement %s says nothing of what the build does with the 404", retired.Operation)
		}
		if len(retired.NeededBy(builds)) == 0 {
			t.Errorf("no installed build calls %s any more: delete its entry from Retired and its contract case", retired.Operation)
		}
	}
}

// Each answer's files carry its marker, and every marker in the server
// names an answer: compatibility code is never kept without saying which
// build needs it, and never listed without saying where it is.
func TestCompatibilityCodeIsMarkedWhereItIs(t *testing.T) {
	known := map[string]Answer{}
	for _, answer := range Answers {
		if _, twice := known[answer.ID]; twice {
			t.Errorf("answer %q is listed twice", answer.ID)
		}
		known[answer.ID] = answer
		if answer.Why == "" || len(answer.Code) == 0 || len(answer.Needs) == 0 {
			t.Errorf("answer %q must say why, where and for which calls", answer.ID)
		}
		for _, file := range answer.Code {
			text, err := os.ReadFile(filepath.Join(moduleRoot, file))
			if err != nil {
				t.Errorf("answer %q names %s: %v", answer.ID, file, err)
				continue
			}
			if !strings.Contains(string(text), Marker(answer.ID)) {
				t.Errorf("%s holds answer %q but has no %q line beside it", file, answer.ID, Marker(answer.ID))
			}
		}
	}

	marked := map[string][]string{}
	err := filepath.WalkDir(moduleRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == "testdata" || entry.Name() == "snapshots" || entry.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		text, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(moduleRoot, path)
		for _, found := range markerLine.FindAllStringSubmatch(string(text), -1) {
			marked[found[1]] = append(marked[found[1]], filepath.ToSlash(relative))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(marked))
	for id := range marked {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		answer, ok := known[id]
		if !ok {
			t.Errorf("%s mark %q, which is no answer in compat.go: register it (which build, which call, why) or remove the code", strings.Join(marked[id], ", "), Marker(id))
			continue
		}
		for _, file := range marked[id] {
			if !contains(answer.Code, file) {
				t.Errorf("%s is marked %q but answer %q does not list it in Code", file, Marker(id), id)
			}
		}
	}
}

func contains(list []string, want string) bool {
	for _, one := range list {
		if one == want {
			return true
		}
	}
	return false
}

func mustLoad(t *testing.T) []Build {
	t.Helper()
	builds, err := All()
	if err != nil {
		t.Fatal(err)
	}
	return builds
}
