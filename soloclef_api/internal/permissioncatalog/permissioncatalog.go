// Package permissioncatalog is app.db.Permission and the descriptions
// app/routes/rbac_routes.py prints beside it: the list of everything a role can
// be given, which the role editor in the web app draws its checkboxes from.
//
// It is a table rather than a generated copy because the Python side is a table
// too: an enum whose declaration order is the response's order, and a dict of
// descriptions that does not cover every member — a permission with no entry is
// described by its own value, which is why three groups read "Permission for
// video_presentations:create" and the response says so.
//
// Two slices read it: this route renders it, and internal/workspaceroles
// refuses a role naming a permission that is not in it. Keeping one copy is the
// point: a role editor offering a permission the write refuses would be a bug
// in two places.
package permissioncatalog

// Permission is one entry of app.db.Permission: the value stored on a role, and
// the enum member's own name, which the response carries as well.
type Permission struct {
	Value string
	Name  string
}

// FullAccess is Permission.FULL_ACCESS, the wildcard no custom role may carry.
const FullAccess = "*"

// GeneralCategory is the category of a permission whose value has no colon in
// it, which is the wildcard alone.
const GeneralCategory = "general"

// All is every permission in the enum's declaration order, which is the order
// of the response.
var All = []Permission{
	{"documents:create", "DOCUMENTS_CREATE"},
	{"documents:read", "DOCUMENTS_READ"},
	{"documents:update", "DOCUMENTS_UPDATE"},
	{"documents:delete", "DOCUMENTS_DELETE"},
	{"artifacts:create", "ARTIFACTS_CREATE"},
	{"artifacts:read", "ARTIFACTS_READ"},
	{"artifacts:update", "ARTIFACTS_UPDATE"},
	{"artifacts:delete", "ARTIFACTS_DELETE"},
	{"chats:create", "CHATS_CREATE"},
	{"chats:read", "CHATS_READ"},
	{"chats:update", "CHATS_UPDATE"},
	{"chats:delete", "CHATS_DELETE"},
	{"comments:create", "COMMENTS_CREATE"},
	{"comments:read", "COMMENTS_READ"},
	{"comments:delete", "COMMENTS_DELETE"},
	{"llm_configs:create", "LLM_CONFIGS_CREATE"},
	{"llm_configs:read", "LLM_CONFIGS_READ"},
	{"llm_configs:update", "LLM_CONFIGS_UPDATE"},
	{"llm_configs:delete", "LLM_CONFIGS_DELETE"},
	{"podcasts:create", "PODCASTS_CREATE"},
	{"podcasts:read", "PODCASTS_READ"},
	{"podcasts:update", "PODCASTS_UPDATE"},
	{"podcasts:delete", "PODCASTS_DELETE"},
	{"video_presentations:create", "VIDEO_PRESENTATIONS_CREATE"},
	{"video_presentations:read", "VIDEO_PRESENTATIONS_READ"},
	{"video_presentations:update", "VIDEO_PRESENTATIONS_UPDATE"},
	{"video_presentations:delete", "VIDEO_PRESENTATIONS_DELETE"},
	{"vision_configs:create", "VISION_CONFIGS_CREATE"},
	{"vision_configs:read", "VISION_CONFIGS_READ"},
	{"vision_configs:delete", "VISION_CONFIGS_DELETE"},
	{"connectors:create", "CONNECTORS_CREATE"},
	{"connectors:read", "CONNECTORS_READ"},
	{"connectors:update", "CONNECTORS_UPDATE"},
	{"connectors:delete", "CONNECTORS_DELETE"},
	{"logs:create", "LOGS_CREATE"},
	{"logs:read", "LOGS_READ"},
	{"logs:update", "LOGS_UPDATE"},
	{"logs:delete", "LOGS_DELETE"},
	{"members:invite", "MEMBERS_INVITE"},
	{"members:view", "MEMBERS_VIEW"},
	{"members:remove", "MEMBERS_REMOVE"},
	{"members:manage_roles", "MEMBERS_MANAGE_ROLES"},
	{"roles:create", "ROLES_CREATE"},
	{"roles:read", "ROLES_READ"},
	{"roles:update", "ROLES_UPDATE"},
	{"roles:delete", "ROLES_DELETE"},
	{"settings:view", "SETTINGS_VIEW"},
	{"settings:update", "SETTINGS_UPDATE"},
	{"settings:delete", "SETTINGS_DELETE"},
	{"api_access:manage", "API_ACCESS_MANAGE"},
	{"public_sharing:view", "PUBLIC_SHARING_VIEW"},
	{"public_sharing:create", "PUBLIC_SHARING_CREATE"},
	{"public_sharing:delete", "PUBLIC_SHARING_DELETE"},
	{"automations:create", "AUTOMATIONS_CREATE"},
	{"automations:read", "AUTOMATIONS_READ"},
	{"automations:update", "AUTOMATIONS_UPDATE"},
	{"automations:delete", "AUTOMATIONS_DELETE"},
	{"automations:execute", "AUTOMATIONS_EXECUTE"},
	{"*", "FULL_ACCESS"},
}

// descriptions is PERMISSION_DESCRIPTIONS. A permission the Python dict does
// not name has no entry here either, so both tiers fall back to the same
// sentence.
var descriptions = map[string]string{
	"documents:create":     "Add new documents, files, and content to the workspace",
	"documents:read":       "View and search documents in the workspace",
	"documents:update":     "Edit existing documents and their metadata",
	"documents:delete":     "Remove documents from the workspace",
	"artifacts:create":     "Create generated artifacts in the workspace",
	"artifacts:read":       "View and download artifacts in the workspace",
	"artifacts:update":     "Revise existing artifacts in the workspace",
	"artifacts:delete":     "Remove artifacts from the workspace",
	"chats:create":         "Start new AI chat conversations",
	"chats:read":           "View chat history and conversations",
	"chats:update":         "Edit chat titles and settings",
	"chats:delete":         "Delete chat conversations",
	"comments:create":      "Add comments and annotations to documents",
	"comments:read":        "View comments on documents",
	"comments:delete":      "Remove comments from documents",
	"llm_configs:create":   "Add new AI model configurations",
	"llm_configs:read":     "View AI model settings and configurations",
	"llm_configs:update":   "Modify AI model configurations",
	"llm_configs:delete":   "Remove AI model configurations",
	"podcasts:create":      "Generate new AI podcasts from content",
	"podcasts:read":        "Listen to and view generated podcasts",
	"podcasts:update":      "Edit podcast settings and metadata",
	"podcasts:delete":      "Remove generated podcasts",
	"connectors:create":    "Set up new data source integrations",
	"connectors:read":      "View configured data sources and their status",
	"connectors:update":    "Modify data source configurations",
	"connectors:delete":    "Remove data source integrations",
	"logs:create":          "Append entries to the activity log",
	"logs:read":            "View activity logs and audit trail",
	"logs:update":          "Rewrite existing activity log entries",
	"logs:delete":          "Clear activity logs",
	"members:invite":       "Send invitations to new team members",
	"members:view":         "View the list of team members",
	"members:remove":       "Remove members from the workspace",
	"members:manage_roles": "Assign and change member roles",
	"roles:create":         "Create new custom roles",
	"roles:read":           "View available roles and their permissions",
	"roles:update":         "Modify role permissions",
	"roles:delete":         "Remove custom roles",
	"settings:view":        "View workspace settings",
	"settings:update":      "Modify workspace settings",
	"settings:delete":      "Delete the entire workspace",
	"api_access:manage":    "Enable or disable programmatic API access for a workspace",
	"automations:create":   "Create automations from chat or JSON",
	"automations:read":     "View automations, their triggers, and run history",
	"automations:update":   "Edit automations and manage their triggers",
	"automations:delete":   "Remove automations from the workspace",
	"automations:execute":  "Manually fire automations",
	"*":                    "Full access to all features and settings",
}

// Describe is the description the route prints: the dict's entry, or the
// fallback the Python route builds from the value itself.
func Describe(value string) string {
	if described, named := descriptions[value]; named {
		return described
	}
	return "Permission for " + value
}

// Category is the part of a value before its colon, or "general" for a value
// with none.
func Category(value string) string {
	for index := 0; index < len(value); index++ {
		if value[index] == ':' {
			return value[:index]
		}
	}
	return GeneralCategory
}

// Known reports whether a value is one of the enum's, which is create_role's
// "Invalid permission" check.
func Known(value string) bool {
	for _, permission := range All {
		if permission.Value == value {
			return true
		}
	}
	return false
}
