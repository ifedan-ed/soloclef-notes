package permissioncatalog

import "git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"

// Listing renders the catalog as the Python route's PermissionsListResponse:
// one object per permission in the enum's order, each with its fields in
// PermissionInfo's field order.
func Listing() *pythonjson.Object {
	entries := make([]any, 0, len(All))
	for _, permission := range All {
		entry := pythonjson.NewObject()
		entry.Set("value", permission.Value)
		entry.Set("name", permission.Name)
		entry.Set("category", Category(permission.Value))
		entry.Set("description", Describe(permission.Value))
		entries = append(entries, entry)
	}

	body := pythonjson.NewObject()
	body.Set("permissions", entries)
	return body
}
