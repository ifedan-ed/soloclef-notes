package myaccess

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/workspaceaccess"
)

// testdata/access.json is four requests to the running Python tier: the rows
// its two queries read, and the response body it returned for them, byte for
// byte. They are the four shapes the answer has — an owner, a member with a
// role, a member with no role, and a workspace open to API keys — because
// is_owner, the role name and the permissions are the only things that vary.
//
// Nothing in it is an account's own text. The capture was taken against a
// database seeded for this comparison, because this repository has a public
// copy: the workspace names and the role names were written for the capture and
// carry a curly quote and an em dash, which is what exercises the JSON writer.
// Nothing was replaced afterwards, so there is no untouched capture that
// differs from this one. custom/docs/go-api-tier.md records how the two tiers
// were compared.

type storedCase struct {
	Name      string `json:"name"`
	Workspace struct {
		Name             string `json:"name"`
		APIAccessEnabled bool   `json:"api_access_enabled"`
	} `json:"workspace"`
	Membership struct {
		IsOwner         bool     `json:"is_owner"`
		RoleName        *string  `json:"role_name"`
		RolePermissions []string `json:"role_permissions"`
	} `json:"membership"`
	WorkspaceID int64  `json:"workspace_id"`
	Response    string `json:"response"`
}

func TestBuildReproducesThePythonResponseByteForByte(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "access.json"))
	if err != nil {
		t.Fatalf("reading the captured responses: %v", err)
	}
	var captured []storedCase
	if err := json.Unmarshal(raw, &captured); err != nil {
		t.Fatalf("parsing the captured responses: %v", err)
	}
	if len(captured) == 0 {
		t.Fatal("the capture is empty")
	}

	for _, capture := range captured {
		t.Run(capture.Name, func(t *testing.T) {
			access := Build(capture.WorkspaceID,
				workspaceaccess.Workspace{
					Exists:           true,
					Name:             capture.Workspace.Name,
					APIAccessEnabled: capture.Workspace.APIAccessEnabled,
				},
				&workspaceaccess.Membership{
					IsOwner:         capture.Membership.IsOwner,
					RoleName:        capture.Membership.RoleName,
					RolePermissions: capture.Membership.RolePermissions,
				})
			got, err := pythonjson.Marshal(access)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(got) != capture.Response {
				t.Errorf("got  %s\nwant %s", got, capture.Response)
			}
		})
	}
}

func TestBuildGivesAnOwnerTheWildcardWhateverTheirRoleHolds(t *testing.T) {
	// get_user_permissions short-circuits on is_owner, so a role with a short
	// list does not narrow an owner.
	name := "Reader"
	got, err := pythonjson.Marshal(Build(7,
		workspaceaccess.Workspace{Exists: true, Name: "Seven"},
		&workspaceaccess.Membership{IsOwner: true, RoleName: &name, RolePermissions: []string{"logs:read"}}))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	const want = `{"workspace_id":7,"workspace_name":"Seven","is_owner":true,` +
		`"role_name":"Reader","permissions":["*"]}`
	if string(got) != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
