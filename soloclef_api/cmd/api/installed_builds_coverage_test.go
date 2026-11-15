package main

import (
	"net/url"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/installedbuilds"
)

// The contract test only proves what it sends. These hold its cases to the
// snapshots without a database, so that a build's call cannot go unsent:
// every operation an installed build calls has a case, every query value a
// build's source pins is sent by one, and no case is about a call no build
// makes.
func TestEveryCallOfAnInstalledBuildIsInTheContract(t *testing.T) {
	builds := loadInstalledBuilds(t)
	cases := map[string][]contractCase{}
	for _, area := range contractAreas() {
		for _, c := range area.Cases {
			cases[installedbuilds.Shape(c.Operation)] = append(cases[installedbuilds.Shape(c.Operation)], c)
		}
	}
	type gap struct{ operation, what string }
	gaps := map[gap][]string{}
	for _, build := range builds {
		for _, call := range build.Calls {
			var sent []contractCase
			for _, c := range cases[installedbuilds.Shape(call.Operation())] {
				if len(c.Bundles) == 0 || slices.Contains(c.Bundles, build.Dir) {
					sent = append(sent, c)
				}
			}
			if len(sent) == 0 {
				gaps[gap{call.Operation(), "no contract case sends it"}] = append(gaps[gap{call.Operation(), "no contract case sends it"}], build.Label())
				continue
			}
			for name, values := range call.Query {
				for _, value := range values {
					if !anySends(sent, name, value) {
						what := "no contract case sends " + name + "=" + value
						gaps[gap{call.Operation(), what}] = append(gaps[gap{call.Operation(), what}], build.Label())
					}
				}
			}
		}
	}
	keys := make([]gap, 0, len(gaps))
	for key := range gaps {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].operation+keys[i].what < keys[j].operation+keys[j].what })
	for _, key := range keys {
		t.Errorf("%s: %s, and %s make that call (%s). Add the request to an area (installed_builds_area_*_test.go).",
			key.operation, key.what, strings.Join(gaps[key], "; "), strings.Join(sitesOf(builds, key.operation), ", "))
	}
}

func anySends(cases []contractCase, name, value string) bool {
	for _, c := range cases {
		_, query, _ := strings.Cut(c.Path, "?")
		values, err := url.ParseQuery(query)
		if err != nil {
			continue
		}
		for _, sent := range values[name] {
			if sent == value {
				return true
			}
		}
	}
	return false
}

// Each case is a call some installed build makes, at a path its template
// matches, and says why when it expects anything but success.
func TestEveryContractCaseIsACallABuildMakes(t *testing.T) {
	builds := loadInstalledBuilds(t)
	called := map[string]bool{}
	for _, build := range builds {
		for _, call := range build.Calls {
			called[installedbuilds.Shape(call.Operation())] = true
		}
	}
	for _, area := range contractAreas() {
		for _, c := range area.Cases {
			name := area.Name + ": " + c.Operation + " " + c.Path
			if !called[installedbuilds.Shape(c.Operation)] {
				t.Errorf("%s: no installed build makes this call", name)
			}
			template := strings.SplitN(c.Operation, " ", 2)[1]
			path, _, _ := strings.Cut(c.Path, "?")
			if !matchesTemplate(template, path) {
				t.Errorf("%s: the path is not one of %s", name, template)
			}
			if (c.Status < 200 || c.Status > 299) && c.Why == "" {
				t.Errorf("%s: expects %d without saying why that is right for the build", name, c.Status)
			}
			if retired, isRetired := installedbuilds.IsRetired(c.Operation); isRetired && c.Status != retired.Status {
				t.Errorf("%s: the call is retired (installedbuilds.Retired), so the build gets %d, not %d", name, retired.Status, c.Status)
			}
			for _, dir := range c.Bundles {
				if !slices.ContainsFunc(builds, func(b installedbuilds.Build) bool { return b.Dir == dir }) {
					t.Errorf("%s: names bundle %s, which has no snapshot", name, dir)
				}
			}
			if c.Unavailable != "" && c.Status < 500 {
				t.Errorf("%s: names an unavailable service but expects %d", name, c.Status)
			}
		}
	}
}

func matchesTemplate(template, path string) bool {
	want, got := strings.Split(template, "/"), strings.Split(path, "/")
	if len(want) != len(got) {
		return false
	}
	for i := range want {
		if strings.HasPrefix(want[i], "{") {
			if got[i] == "" {
				return false
			}
			continue
		}
		if want[i] != got[i] {
			return false
		}
	}
	return true
}

// CI runs the contract with its database and Valkey, and fails rather than
// skips when they are missing: the workflow sets all three variables for
// soloclef_api. A workflow edited to drop them fails here first.
func TestCIRunsTheInstalledBuildsContract(t *testing.T) {
	workflow, err := os.ReadFile("../../../.forgejo/workflows/go.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, variable := range []string{contractDatabase, contractValkey, contractRequired + ": \"true\""} {
		if !strings.Contains(string(workflow), variable) {
			t.Errorf(".forgejo/workflows/go.yml does not set %s for the installed builds' contract", variable)
		}
	}
}
