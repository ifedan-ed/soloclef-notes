package toolmanifest_test

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/toolmanifest"
)

// manifest is the tools/list this server publishes. The tool names, parameter
// names and descriptions in it are the contract a client has in its
// configuration, so this file is the specification and this test is what
// proves nothing moved by accident. It was first captured from the Python
// server; it changes now only on purpose, together with connect-your-agent.mdx.
const manifest = "testdata/tools_list.json"

func TestPublishedSurfaceIsTheManifest(t *testing.T) {
	expected := loadManifest(t)
	published := publishedByName(t)

	if len(published) != len(expected) {
		t.Fatalf("published %d tools, the manifest lists %d",
			len(published), len(expected))
	}
	for name, want := range expected {
		got, present := published[name]
		if !present {
			t.Errorf("tool %s is not published any more", name)
			continue
		}
		for _, field := range []string{"name", "title", "description", "annotations", "inputSchema"} {
			if !reflect.DeepEqual(got[field], want[field]) {
				t.Errorf("%s.%s differs\n got: %s\nwant: %s",
					name, field, mustJSON(t, got[field]), mustJSON(t, want[field]))
			}
		}
		for field := range got {
			if _, known := want[field]; !known {
				t.Errorf("%s carries an extra field %q the manifest does not: %s",
					name, field, mustJSON(t, got[field]))
			}
		}
	}
	for name := range published {
		if _, known := expected[name]; !known {
			t.Errorf("tool %s is published but is not in the manifest", name)
		}
	}
}

func TestToolsAreListedInRegistrationOrder(t *testing.T) {
	expected := manifestOrder(t)
	tools, err := toolmanifest.Collect()
	if err != nil {
		t.Fatal(err)
	}
	var published []string
	for _, tool := range tools {
		published = append(published, tool.Name)
	}
	if !reflect.DeepEqual(published, expected) {
		t.Errorf("listed in a different order\n got: %v\nwant: %v", published, expected)
	}
	sorted := append([]string(nil), published...)
	sort.Strings(sorted)
	if reflect.DeepEqual(sorted, published) {
		t.Error("the list is in name order, so this test could not tell the " +
			"registration order from the SDK's own sorting")
	}
}

func TestManifestHasNoProblems(t *testing.T) {
	tools, err := toolmanifest.Collect()
	if err != nil {
		t.Fatal(err)
	}
	if problems := toolmanifest.Problems(tools); len(problems) > 0 {
		t.Errorf("manifest problems: %v", problems)
	}
}

func TestEveryExpectedToolIsPublished(t *testing.T) {
	published := publishedByName(t)
	for _, name := range toolmanifest.Expected {
		if _, ok := published[name]; !ok {
			t.Errorf("expected tool %s is not published", name)
		}
	}
	if len(toolmanifest.Expected) != len(published) {
		t.Errorf("%d expected names, %d published tools",
			len(toolmanifest.Expected), len(published))
	}
}

// publishedByName round-trips each tool through JSON, so what is compared is the
// payload a client receives rather than the Go value behind it.
func publishedByName(t *testing.T) map[string]map[string]any {
	t.Helper()
	tools, err := toolmanifest.Collect()
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]map[string]any{}
	for _, tool := range tools {
		byName[tool.Name] = asMap(t, tool)
	}
	return byName
}

func loadManifest(t *testing.T) map[string]map[string]any {
	t.Helper()
	var captured []map[string]any
	readJSON(t, manifest, &captured)
	byName := map[string]map[string]any{}
	for _, tool := range captured {
		byName[tool["name"].(string)] = tool
	}
	return byName
}

func manifestOrder(t *testing.T) []string {
	t.Helper()
	var captured []map[string]any
	readJSON(t, manifest, &captured)
	var order []string
	for _, tool := range captured {
		order = append(order, tool["name"].(string))
	}
	return order
}

func readJSON(t *testing.T, path string, into any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatal(err)
	}
}

func asMap(t *testing.T, tool *mcp.Tool) map[string]any {
	t.Helper()
	raw, err := json.Marshal(tool)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
