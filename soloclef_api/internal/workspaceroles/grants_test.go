package workspaceroles

import (
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/workspaceaccess"
)

func member(owner bool, permissions ...string) *workspaceaccess.Membership {
	return &workspaceaccess.Membership{IsOwner: owner, RolePermissions: permissions}
}

// role_permissions_allowed reads stricter than its name: holding roles:create
// is not enough, and the wildcard is never writable. Both halves were checked
// against the Python tier, which refuses a member with roles:create in the same
// words as it refuses a wildcard.
func TestOnlyAnOwnerMayWriteARoleAndNeverTheWildcard(t *testing.T) {
	cases := []struct {
		name        string
		actor       *workspaceaccess.Membership
		permissions []string
		allowed     bool
	}{
		{"the owner, ordinary permissions", member(true), []string{"roles:read"}, true},
		{"the owner, no permissions at all", member(true), nil, true},
		{"the owner, the wildcard", member(true), []string{"*"}, false},
		{"the owner, the wildcard among others", member(true), []string{"roles:read", "*"}, false},
		{"a member holding roles:create", member(false, "roles:create"), []string{"roles:read"}, false},
		{"a member holding the wildcard in their own role",
			member(false, "*"), []string{"roles:read"}, false},
		{"a member with no role", member(false), []string{"roles:read"}, false},
		{"nobody", nil, []string{"roles:read"}, false},
	}
	for _, test := range cases {
		if got := MayWriteRole(test.actor, test.permissions); got != test.allowed {
			t.Errorf("%s: allowed=%v, want %v", test.name, got, test.allowed)
		}
	}
}

// The Python loop names the first permission it does not recognise, so the
// refusal a request with two bad ones gets is the same on both tiers.
func TestTheFirstUnknownPermissionIsTheOneNamed(t *testing.T) {
	if _, found := UnknownPermission([]string{"roles:read", "documents:read"}); found {
		t.Error("a known permission was refused")
	}
	unknown, found := UnknownPermission([]string{"roles:read", "documents:fly", "also:wrong"})
	if !found || unknown != "documents:fly" {
		t.Errorf("named %q (%v), want documents:fly", unknown, found)
	}
	if unknown, _ := UnknownPermission([]string{"*"}); unknown != "" {
		t.Errorf("the wildcard is a permission of the enum, not an unknown one: %q", unknown)
	}
}
