package workspaceaccess

import (
	"net/http"
	"testing"
)

const denial = "You don't have permission to read logs in this workspace"

var liveWorkspace = Workspace{Exists: true, APIAccessEnabled: true}

func TestAnOwnerAlwaysHasAccess(t *testing.T) {
	if refusal := CheckMembership(&Membership{IsOwner: true}, LogsRead, denial); refusal != nil {
		t.Errorf("got %+v, want no refusal", refusal)
	}
}

func TestARoleNeedsThePermissionOrTheWildcard(t *testing.T) {
	for name, permissions := range map[string][]string{
		"the permission itself": {"documents:read", LogsRead},
		"the wildcard":          {FullAccess},
	} {
		if refusal := CheckMembership(&Membership{RolePermissions: permissions}, LogsRead, denial); refusal != nil {
			t.Errorf("%s: got %+v", name, refusal)
		}
	}

	for name, permissions := range map[string][]string{
		"another permission": {"documents:read"},
		"no role at all":     nil,
	} {
		refusal := CheckMembership(&Membership{RolePermissions: permissions}, LogsRead, denial)
		if refusal == nil || refusal.Status != http.StatusForbidden || refusal.Message != denial {
			t.Errorf("%s: got %+v, want the route's own 403", name, refusal)
		}
	}
}

func TestANonMemberIsToldNothingAboutTheWorkspace(t *testing.T) {
	// The membership check runs before the workspace is ever looked up, so a
	// workspace that does not exist cannot be told apart from one the caller
	// is simply not in.
	refusal := CheckMembership(nil, LogsRead, denial)
	if refusal == nil || refusal.Status != http.StatusForbidden ||
		refusal.Message != "You don't have access to this workspace" {
		t.Errorf("got %+v", refusal)
	}
}

func TestAMemberOfAMissingWorkspaceGetsTheNotFound(t *testing.T) {
	refusal := CheckAPIAccess(Workspace{Exists: false}, false)
	if refusal == nil || refusal.Status != http.StatusNotFound || refusal.Message != "Workspace not found" {
		t.Errorf("got %+v", refusal)
	}
}

func TestAnAPIKeyOnlySeesWorkspacesWithAPIAccessOn(t *testing.T) {
	closed := Workspace{Exists: true, APIAccessEnabled: false}

	if refusal := CheckAPIAccess(closed, false); refusal != nil {
		t.Errorf("a session must not be gated: %+v", refusal)
	}
	if refusal := CheckAPIAccess(liveWorkspace, true); refusal != nil {
		t.Errorf("an open workspace must not be refused: %+v", refusal)
	}

	refusal := CheckAPIAccess(closed, true)
	if refusal == nil || refusal.Status != http.StatusForbidden ||
		refusal.Message != "API access is not enabled for this workspace." {
		t.Errorf("got %+v", refusal)
	}
}

func TestCheckAccessOnlyAsksForMembership(t *testing.T) {
	// check_workspace_access requires no particular permission, so a member
	// with none passes it.
	if refusal := CheckAccess(&Membership{}); refusal != nil {
		t.Errorf("a member was refused: %+v", refusal)
	}
	refusal := CheckAccess(nil)
	if refusal == nil || refusal.Status != http.StatusForbidden ||
		refusal.Message != "You don't have access to this workspace" {
		t.Errorf("got %+v", refusal)
	}
}

func TestEffectivePermissionsIsGetUserPermissions(t *testing.T) {
	name := "Reader"
	for _, example := range []struct {
		why        string
		membership *Membership
		want       []string
	}{
		{"no membership", nil, nil},
		{"an owner holds the wildcard", &Membership{IsOwner: true}, []string{FullAccess}},
		{"an owner's role does not narrow it",
			&Membership{IsOwner: true, RoleName: &name, RolePermissions: []string{"logs:read"}},
			[]string{FullAccess}},
		{"a role's permissions",
			&Membership{RoleName: &name, RolePermissions: []string{"logs:read", "members:view"}},
			[]string{"logs:read", "members:view"}},
		{"no role, nothing granted", &Membership{}, nil},
	} {
		got := EffectivePermissions(example.membership)
		if len(got) != len(example.want) {
			t.Errorf("%s: got %v, want %v", example.why, got, example.want)
			continue
		}
		for i := range got {
			if got[i] != example.want[i] {
				t.Errorf("%s: got %v, want %v", example.why, got, example.want)
				break
			}
		}
	}
}
