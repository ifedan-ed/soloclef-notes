package workspacemembers

import (
	"time"

	"git.danvics.com/danvics/surfsense/soloclef_api/internal/pythonjson"
)

// Build renders the roster as the Python route's list[MembershipRead].
//
// The keys are in pydantic's field order, which is the order of the response's
// bytes, and the datetimes are in pydantic's own form rather than isoformat's,
// because the route declares a response_model (internal/pythonjson).
func Build(roster Roster) []any {
	members := make([]any, 0, len(roster.Memberships))
	for _, membership := range roster.Memberships {
		member := pythonjson.NewObject()
		member.Set("id", membership.ID)
		member.Set("user_id", membership.UserID)
		member.Set("workspace_id", membership.WorkspaceID)
		member.Set("role_id", numberOrNil(membership.RoleID))
		member.Set("is_owner", membership.IsOwner)
		member.Set("joined_at", pythonjson.PydanticDateTime(membership.JoinedAt))
		member.Set("created_at", pythonjson.PydanticDateTime(membership.CreatedAt))
		member.Set("role", role(roster, membership.RoleID))

		account, known := roster.Accounts[membership.UserID]
		if !known {
			member.Set("user_email", nil)
			member.Set("user_display_name", nil)
			member.Set("user_avatar_url", nil)
			member.Set("user_last_login", nil)
		} else {
			member.Set("user_email", account.Email)
			member.Set("user_display_name", textOrNil(account.DisplayName))
			member.Set("user_avatar_url", textOrNil(account.AvatarURL))
			member.Set("user_last_login", momentOrNil(account.LastLogin))
		}
		members = append(members, member)
	}
	return members
}

// role is `"role": membership.role`: the loaded role, or null when the
// membership points at none.
func role(roster Roster, roleID *int64) any {
	if roleID == nil {
		return nil
	}
	found, known := roster.Roles[*roleID]
	if !known {
		return nil
	}
	rendered := pythonjson.NewObject()
	rendered.Set("name", found.Name)
	rendered.Set("description", textOrNil(found.Description))
	permissions := make([]any, 0, len(found.Permissions))
	for _, permission := range found.Permissions {
		permissions = append(permissions, permission)
	}
	rendered.Set("permissions", permissions)
	rendered.Set("is_default", found.IsDefault)
	rendered.Set("id", found.ID)
	rendered.Set("workspace_id", found.WorkspaceID)
	rendered.Set("is_system_role", found.IsSystemRole)
	rendered.Set("created_at", pythonjson.PydanticDateTime(found.CreatedAt))
	return rendered
}

func numberOrNil(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func textOrNil(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func momentOrNil(value *time.Time) any {
	if value == nil {
		return nil
	}
	return pythonjson.PydanticDateTime(*value)
}
