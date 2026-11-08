package workspaceroles

import "git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"

// Build renders one role as the Python routes' RoleRead.
//
// The keys are pydantic's field order, which puts RoleBase's four first and
// RoleRead's own four after them, and the datetime is pydantic's own form
// rather than isoformat's because every route here declares a response_model
// (internal/pythonjson).
func Build(role Role) *pythonjson.Object {
	rendered := pythonjson.NewObject()
	rendered.Set("name", role.Name)
	rendered.Set("description", textOrNil(role.Description))
	permissions := make([]any, 0, len(role.Permissions))
	for _, permission := range role.Permissions {
		permissions = append(permissions, permission)
	}
	rendered.Set("permissions", permissions)
	rendered.Set("is_default", role.IsDefault)
	rendered.Set("id", role.ID)
	rendered.Set("workspace_id", role.WorkspaceID)
	rendered.Set("is_system_role", role.IsSystemRole)
	rendered.Set("created_at", pythonjson.PydanticDateTime(role.CreatedAt))
	return rendered
}

// BuildList renders the list route's list[RoleRead], in the order the query
// returned the rows.
func BuildList(roles []Role) []any {
	rendered := make([]any, 0, len(roles))
	for _, role := range roles {
		rendered = append(rendered, Build(role))
	}
	return rendered
}

func textOrNil(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}
