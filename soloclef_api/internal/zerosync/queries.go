package zerosync

import (
	"encoding/json"
	"fmt"
)

// The AST in zero-protocol's shape (ast.ts), with server names: tables and
// columns as Postgres has them, which is what zero-cache expects back
// (Zero's handler maps client names to server names before answering, so
// these are written already mapped).
type ast struct {
	Table   string      `json:"table"`
	Alias   string      `json:"alias,omitempty"`
	Where   any         `json:"where,omitempty"`
	OrderBy [][2]string `json:"orderBy,omitempty"`
	// Limit is 1 for a .one() query, absent otherwise.
	Limit int `json:"limit,omitempty"`
}

type column struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

type literal struct {
	Type  string `json:"type"`
	Value any    `json:"value"`
}

type simple struct {
	Type  string  `json:"type"`
	Left  column  `json:"left"`
	Right literal `json:"right"`
	Op    string  `json:"op"`
}

type and struct {
	Type       string `json:"type"`
	Conditions []any  `json:"conditions"`
}

type correlation struct {
	ParentField []string `json:"parentField"`
	ChildField  []string `json:"childField"`
}

type related struct {
	System      string      `json:"system"`
	Correlation correlation `json:"correlation"`
	Subquery    ast         `json:"subquery"`
}

type exists struct {
	Type    string  `json:"type"`
	Related related `json:"related"`
	Op      string  `json:"op"`
}

func equals(name string, value any) simple {
	return simple{Type: "simple", Left: column{Type: "column", Name: name}, Right: literal{Type: "literal", Value: value}, Op: "="}
}

// owned is constrainToOwner (ownership.ts): the row's user_id is the caller.
func owned(userID string) simple { return equals("user_id", userID) }

// parent is a child table's way to its owning row, for the tables that carry
// no owner of their own (constrainThroughParent): the relationship's name in
// the web's schema (Zero names the subquery "zsubq_" + it), the child's
// column that points at the parent, and the parent's table.
type parent struct {
	relationship string
	childColumn  string
	table        string
}

// shape is one named query: soloclef_web/zero/queries, row for row.
type shape struct {
	table string
	// arg is the by-id argument's name ("threadId"), empty for the account's
	// whole list; argColumn is the column it is compared with.
	arg       string
	argColumn string
	// through is set for a child table; nil means the table has user_id.
	through *parent
	orderBy [][2]string
	one     bool
}

var (
	thread     = &parent{relationship: "thread", childColumn: "thread_id", table: "new_chat_threads"}
	automation = &parent{relationship: "automation", childColumn: "automation_id", table: "automations"}
	newest     = [][2]string{{"created_at", "desc"}}
)

// shapes is every query the web app names (zero/queries/index.ts).
var shapes = map[string]shape{
	"notifications.mine": {table: "notifications", orderBy: newest},
	"documents.mine":     {table: "documents", orderBy: newest},
	"folders.mine":       {table: "folders", orderBy: [][2]string{{"position", "asc"}}},
	"connectors.mine":    {table: "search_source_connectors", orderBy: newest},
	"messages.byThread": {table: "new_chat_messages", arg: "threadId", argColumn: "thread_id",
		through: thread, orderBy: [][2]string{{"created_at", "asc"}}},
	"chatSession.byThread": {table: "chat_session_state", arg: "threadId", argColumn: "thread_id",
		through: thread, one: true},
	"automationRuns.byAutomation": {table: "automation_runs", arg: "automationId", argColumn: "automation_id",
		through: automation, orderBy: newest},
	"podcastRuns.byId":     {table: "podcast_runs", arg: "podcastId", argColumn: "id", one: true},
	"deliverableJobs.byId": {table: "deliverable_jobs", arg: "jobId", argColumn: "id", one: true},
}

// build is the named query's AST for the caller. userID is the gate's, and
// is the only account any AST names.
func build(name string, args []json.RawMessage, userID string) (ast, error) {
	s, known := shapes[name]
	if !known {
		return ast{}, fmt.Errorf("Query not found: %s", name)
	}
	var conditions []any
	if s.arg != "" {
		id, err := integerArg(args, s.arg)
		if err != nil {
			return ast{}, fmt.Errorf("Validation failed for query %s: %v", name, err)
		}
		conditions = append(conditions, equals(s.argColumn, id))
	}
	if s.through == nil {
		conditions = append(conditions, owned(userID))
	} else {
		conditions = append(conditions, exists{
			Type: "correlatedSubquery",
			Op:   "EXISTS",
			Related: related{
				System:      "client",
				Correlation: correlation{ParentField: []string{s.through.childColumn}, ChildField: []string{"id"}},
				Subquery: ast{
					Table: s.through.table,
					Alias: "zsubq_" + s.through.relationship,
					Where: owned(userID),
				},
			},
		})
	}
	built := ast{Table: s.table, OrderBy: s.orderBy}
	// One condition stands alone; Zero writes two or more as an "and".
	if len(conditions) == 1 {
		built.Where = conditions[0]
	} else {
		built.Where = and{Type: "and", Conditions: conditions}
	}
	if s.one {
		built.Limit = 1
	}
	return built, nil
}
