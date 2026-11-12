// Package agentrules serves an account's agent permission rules: the
// standing answers to "may the assistant run this tool", which are allow,
// deny or ask, for every chat of the account or for one thread.
//
//	GET    /api/v1/agent/permissions/rules            the account's rules
//	POST   /api/v1/agent/permissions/rules            write one
//	PATCH  /api/v1/agent/permissions/rules/{rule_id}  change its pattern or action
//	DELETE /api/v1/agent/permissions/rules/{rule_id}  remove it
//
// The table is the one the chat reads at the moment it decides a call and
// writes when a person answers "always allow" (internal/chat, ToolIsTrusted
// and TrustTool), so a rule made here is honoured by the next call and a
// rule made there is listed here.
//
// # Whose a rule is
//
// The caller's, always. The account comes from the request's principal and
// from nowhere else: no path, query or body names it, and a body that
// carries a user_id is read past. Every statement names the account beside
// the rule's id (queries.sql), so another account's rule is "Rule not
// found", the same answer as for a rule that was never written, and a rule
// can only be scoped to a thread that is the caller's own.
//
// # The feature flag
//
// All four routes answer 503 naming the setting while the feature is
// switched off for the deployment, so the settings page can draw an empty
// state instead of breaking on a missing route (flags.go).
//
// # What this does better than the routes it replaces
//
//   - A duplicate is a duplicate. The unique constraint now compares a NULL
//     thread as equal to a NULL thread, so an account-wide rule cannot be
//     stored twice; it used to be storable any number of times.
//   - Only a duplicate is reported as one. Every integrity failure used to
//     be answered "an identical rule already exists", a foreign key
//     included.
//   - A thread id no thread can have is "Thread not found". It used to be
//     a 500.
//   - The update and the delete are one statement each, held to the
//     account. They were a read and then a write.
package agentrules

import (
	"regexp"
	"time"
)

// The paths.
const (
	RulesPath = "/api/v1/agent/permissions/rules"
	RulePath  = "/api/v1/agent/permissions/rules/{rule_id}"
)

// Actions is what a rule may answer, in the order a refusal names them.
var Actions = []string{"allow", "deny", "ask"}

// DefaultPattern is the pattern of a rule that gives none, and what a
// pattern of only white space becomes: every call of the tool.
const DefaultPattern = "*"

// The bounds of the two text fields: the columns' own varchar(255).
const (
	textMinLength = 1
	textMaxLength = 255
)

// PermissionPattern is what a permission (a tool's or a capability's name,
// with * wildcards) may be made of.
var PermissionPattern = regexp.MustCompile(`^[a-zA-Z0-9_:.\-*]+$`)

// What a person is told.
const (
	// flagOff names the setting, which is what lets an operator read the
	// answer and act on it.
	flagOff = "Agent permission rules are not enabled on this deployment. " +
		"Flip SOLOCLEF_ENABLE_PERMISSION to enable them."
	permissionMalformed = "permission must contain only alphanumerics, '.', '_', ':', '-', " +
		"or '*' wildcards."
	// threadAbsent is one sentence for a thread that is not there and for
	// one that is another account's, so it says nothing about which.
	threadAbsent      = "Thread not found."
	ruleAbsent        = "Rule not found."
	duplicateOnCreate = "An identical rule already exists for this scope. Update the " +
		"existing rule instead."
	duplicateOnUpdate = "Update would create a duplicate rule for this scope."
)

// Rule is an agent_permission_rules row of the caller's.
type Rule struct {
	ID int64
	// ThreadID is the one thread the rule applies in, or nil for a rule
	// that applies in every chat of the account.
	ThreadID *int64
	// Permission is the tool or capability the rule is about.
	Permission string
	Pattern    string
	Action     string
	CreatedAt  time.Time
}
