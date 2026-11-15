package main

import (
	"sort"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/installedbuilds"
)

// These tests need no database: they hold the server's route table to what
// the phone builds already installed from the App Store call
// (internal/installedbuilds). installed_builds_contract_test.go then sends
// those calls through the real server and a real Postgres.

// Every route an installed build calls is one this server answers. A route
// removed from servedRoutes() is a 404 on every phone running that build,
// which is how 1.1.0's share menu and Public links page broke.
func TestEveryRouteAnInstalledBuildCallsIsServed(t *testing.T) {
	builds := loadInstalledBuilds(t)
	served := map[string]bool{}
	for _, route := range servedRoutes() {
		served[installedbuilds.Shape(route)] = true
	}
	missing := map[string][]string{}
	for _, build := range builds {
		for _, call := range build.Calls {
			if _, retired := installedbuilds.IsRetired(call.Operation()); retired {
				continue
			}
			if !served[installedbuilds.Shape(call.Operation())] {
				missing[call.Operation()] = append(missing[call.Operation()], build.Label())
			}
		}
	}
	operations := make([]string, 0, len(missing))
	for operation := range missing {
		operations = append(operations, operation)
	}
	sort.Strings(operations)
	for _, operation := range operations {
		builds := missing[operation]
		sites := sitesOf(loadInstalledBuilds(t), operation)
		t.Errorf("%s is not served, and these installed builds call it (a 404 on the phone): %s. The app calls it from %s. "+
			"Serve it again, answer it for these builds (installedbuilds.Answers), retire the call where the 404 is right "+
			"for the build (installedbuilds.Retired), or retire the snapshots (internal/installedbuilds/README.md).", operation, strings.Join(builds, "; "), strings.Join(sites, ", "))
	}
	for _, retired := range installedbuilds.Retired {
		if served[installedbuilds.Shape(retired.Operation)] {
			t.Errorf("%s is served again, but installedbuilds.Retired says the builds get a 404 for it: remove the retirement and expect the answer in its contract case", retired.Operation)
		}
	}
}

func loadInstalledBuilds(t *testing.T) []installedbuilds.Build {
	t.Helper()
	builds, err := installedbuilds.All()
	if err != nil {
		t.Fatal(err)
	}
	return builds
}

// sitesOf is where the apps call an operation, for a failure message.
func sitesOf(builds []installedbuilds.Build, operation string) []string {
	seen := map[string]bool{}
	var out []string
	for _, build := range builds {
		for _, call := range build.Calls {
			if call.Operation() != operation {
				continue
			}
			for _, site := range call.Sites {
				if !seen[site] {
					seen[site] = true
					out = append(out, site)
				}
			}
		}
	}
	return out
}
