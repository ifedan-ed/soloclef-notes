package workspaceroles

import (
	"os"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// seeded is the four roles of the comparison workspace, as the queries read
// them, in the order the capture beside them came back in. The text is the
// seed's own and names no account: the em dash and the curly quotes are there
// because they decide the bytes.
//
// The order is the capture's and not a promise. Neither tier pins it — neither
// query has an ORDER BY — and Postgres does not either: the same question
// against the same four rows came back in id order on a small table and in name
// order once the table had grown enough for the planner to prefer the
// (workspace_id, name) index. So what this fixture pins is the rendering of a
// given order, and what makes the two tiers agree is that they ask the same
// unordered question at the same moment (custom/docs/go-api-tier.md, "The array
// order").
func seeded() []Role {
	description := func(text string) *string { return &text }
	moment := func(text string) time.Time {
		parsed, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			panic(err)
		}
		return parsed
	}
	return []Role{
		{Name: "Owner", Description: description("Full access"), Permissions: []string{"*"},
			IsDefault: false, ID: 1, WorkspaceID: 1, IsSystemRole: true,
			CreatedAt: moment("2026-10-01T00:00:00Z")},
		{Name: "Viewer", Description: description("Read only — the seeded default"),
			Permissions: []string{"documents:read", "members:view", "roles:read"},
			IsDefault:   true, ID: 2, WorkspaceID: 1, IsSystemRole: true,
			CreatedAt: moment("2026-10-01T00:00:00.5Z")},
		{Name: "Role Wrangler", Description: description("May write roles"),
			Permissions: []string{"roles:create", "roles:read", "roles:update", "roles:delete", "members:view"},
			IsDefault:   false, ID: 3, WorkspaceID: 1, IsSystemRole: false,
			CreatedAt: moment("2026-10-02T00:00:00Z")},
		{Name: "Plain Reader", Description: description("Cannot touch roles"),
			Permissions: []string{"documents:read", "members:view"},
			IsDefault:   false, ID: 4, WorkspaceID: 1, IsSystemRole: false,
			CreatedAt: moment("2026-10-03T00:00:00Z")},
	}
}

// testdata/roles.json is the body the Python tier returned for
// GET /api/v1/workspaces/1/roles against those rows, captured over HTTP. It
// pins pydantic's datetime — the half second written as .500000 and the whole
// second written without a fraction at all — and the key order of RoleRead.
func TestTheRoleListIsThePythonTiersBytes(t *testing.T) {
	want, err := os.ReadFile("testdata/roles.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := pythonjson.Marshal(BuildList(seeded()))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("the role list is not the captured bytes\n got %s\nwant %s", got, want)
	}
}

// One role on its own is the same object, not a list of one.
// roleNamed picks one of the seeded roles by name rather than by position,
// because the slice's order is the capture's and not meaningful.
func roleNamed(name string) Role {
	for _, role := range seeded() {
		if role.Name == name {
			return role
		}
	}
	panic("no seeded role named " + name)
}

func TestOneRoleIsThePythonTiersBytes(t *testing.T) {
	want, err := os.ReadFile("testdata/role.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := pythonjson.Marshal(Build(roleNamed("Role Wrangler")))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("the role is not the captured bytes\n got %s\nwant %s", got, want)
	}
}

// A description column holding NULL is JSON null, not the empty string, and an
// empty permissions array is [] rather than null.
func TestTheAbsentValuesAreNullAndEmpty(t *testing.T) {
	got, err := pythonjson.Marshal(Build(Role{Name: "Bare", ID: 6, WorkspaceID: 1,
		Permissions: []string{}, CreatedAt: time.Date(2026, 11, 8, 4, 55, 42, 142756000, time.UTC)}))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"Bare","description":null,"permissions":[],"is_default":false,` +
		`"id":6,"workspace_id":1,"is_system_role":false,"created_at":"2026-11-08T04:55:42.142756Z"}`
	if string(got) != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}
