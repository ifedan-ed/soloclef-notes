package livechanges

import (
	"net/http"

	lc "git.danvics.com/danvics/soloclef/soloclef_notify/livechange"
)

// Rules is what each write route changes, for Writes. Every served write
// route must match one rule (cmd/api's served_test.go holds it to that).
//
// Read it by prefix: the longest matching prefix wins. Where a route's write
// reaches more than one kind of thing (removing a folder removes its
// documents; saving a note is saving a document) it names each, because the
// clients' lists are by kind. Over-telling costs one REST read of a list
// that is open; under-telling leaves a screen stale until focus.
var Rules = []Rule{
	// Nothing a client watches live: sign-in and account plumbing, keys,
	// searches that write no row, model settings, study progress, feedback
	// and logs. Said, so that no route is left out by accident.
	{Prefix: "/auth"},
	{Prefix: "/users/me"},
	{Prefix: "/api/v1/auth"},
	{Prefix: "/api/v1/users/me"},
	{Prefix: "/api/v1/pats"},
	{Prefix: "/api/v1/push"},
	{Prefix: "/api/v1/presence"},
	{Prefix: "/api/v1/prompts"},
	{Prefix: "/api/v1/feedback"},
	{Prefix: "/api/v1/client-errors"},
	{Prefix: "/api/v1/logs"},
	{Prefix: "/api/v1/model-connections"},
	{Prefix: "/api/v1/models"},
	{Prefix: "/api/v1/system-instructions"},
	{Prefix: "/api/v1/agent/permissions"},
	{Prefix: "/api/v1/academic-search"},
	{Prefix: "/api/v1/documents/search-semantic"},
	{Prefix: "/api/v1/notebook-report-templates"},
	{Prefix: "/api/v1/snapshots"},
	{Prefix: "/api/v1/threads/{thread_id}/snapshots"},
	{Prefix: "/api/v1/artifacts/media-links"},
	{Prefix: "/api/v1/app/handoff"},
	{Prefix: "/api/v1/notebooks/{notebook_id}/followups"},
	{Prefix: "/api/v1/notebooks/{notebook_id}/research/search"},
	{Prefix: "/api/v1/connectors/{connector_id}/nextcloud/import/preview"},
	{Prefix: "/api/v1/connectors/email-imap/test"},
	{Prefix: "/api/v1/connectors/email-imap/accounts/{account_id}/test"},
	{Prefix: "/api/v1/connectors/jmap/accounts/{account_id}/test"},
	{Prefix: "/api/v1/connectors/mcp/test"},
	{Prefix: "/api/v1/connectors/mcp/oauth/discover"},
	{Prefix: "/api/v1/connectors/mcp/{connector_id}/oauth/start"},
	// A flashcard's or a quiz's progress is the study screen's own, not a
	// list anything else shows.
	{Prefix: "/api/v1/artifacts/{artifact_id}/flashcard-order"},
	{Prefix: "/api/v1/artifacts/{artifact_id}/flashcard-progress"},
	{Prefix: "/api/v1/artifacts/{artifact_id}/quiz-answer"},
	{Prefix: "/api/v1/artifacts/{artifact_id}/quiz-skip"},
	{Prefix: "/api/v1/artifacts/{artifact_id}/quiz-retake"},

	// Documents and folders.
	{Prefix: "/api/v1/documents", Changes: []Template{{Kind: lc.Documents}}},
	{Prefix: "/api/v1/documents/{document_id}", Changes: []Template{{Kind: lc.Documents, IDFrom: "document_id"}}},
	// A folder upload makes the folders it carries.
	{Prefix: "/api/v1/documents/folder-upload", Changes: []Template{{Kind: lc.Documents}, {Kind: lc.Folders}}},
	// Removing, moving or watching a folder reaches the documents in it.
	{Prefix: "/api/v1/folders", Changes: []Template{{Kind: lc.Folders}, {Kind: lc.Documents}}},
	{Prefix: "/api/v1/folders/{folder_id}", Changes: []Template{{Kind: lc.Folders, IDFrom: "folder_id"}, {Kind: lc.Documents}}},
	{Prefix: "/api/v1/app/handoff/drive-picker/import", Changes: []Template{{Kind: lc.Documents}}},

	// Notes: a note is a document too, and its notebook links are sources.
	{Prefix: "/api/v1/notes", Changes: []Template{{Kind: lc.Notes}, {Kind: lc.Documents}}},
	{Prefix: "/api/v1/notes/{note_id}", Changes: []Template{
		{Kind: lc.Notes, IDFrom: "note_id"}, {Kind: lc.Documents, IDFrom: "note_id"}}},
	{Prefix: "/api/v1/notes/{note_id}/notebook", Changes: []Template{
		{Kind: lc.Notes, IDFrom: "note_id"}, {Kind: lc.Documents, IDFrom: "note_id"}, {Kind: lc.Notebooks}}},
	{Prefix: "/api/v1/notes/{note_id}/sources/{notebook_id}", Changes: []Template{
		{Kind: lc.Notes, IDFrom: "note_id"}, {Kind: lc.Notebooks, IDFrom: "notebook_id"}}},

	// Notebooks: the row, its sources, its links, its copies.
	{Prefix: "/api/v1/notebooks", Changes: []Template{{Kind: lc.Notebooks}}},
	{Prefix: "/api/v1/notebooks/{notebook_id}", Changes: []Template{{Kind: lc.Notebooks, IDFrom: "notebook_id"}}},
	{Prefix: "/api/v1/notebook-links", Changes: []Template{{Kind: lc.Notebooks}}},
	{Prefix: "/api/v1/notebooks/{notebook_id}/copy", Changes: []Template{{Kind: lc.Notebooks}, {Kind: lc.Documents}}},
	// An imported or read page is a new document and a new source.
	{Prefix: "/api/v1/notebooks/{notebook_id}/research", Changes: []Template{
		{Kind: lc.Notebooks, IDFrom: "notebook_id"}, {Kind: lc.Documents}}},
	{Prefix: "/api/v1/notebooks/{notebook_id}/studio", Changes: []Template{{Kind: lc.Outputs, NotebookIDFrom: "notebook_id"}}},
	{Prefix: "/api/v1/notebooks/{notebook_id}/threads", Changes: []Template{{Kind: lc.Chats, NotebookIDFrom: "notebook_id"}}},
	// A public notebook or chat taken into the account.
	{Prefix: "/api/v1/public", Changes: []Template{{Kind: lc.Notebooks}, {Kind: lc.Chats}, {Kind: lc.Documents}}},

	// Studio outputs made outside a notebook's own routes.
	{Prefix: "/api/v1/podcasts", Changes: []Template{{Kind: lc.Outputs}}},
	{Prefix: "/api/v1/deliverable-jobs", Changes: []Template{{Kind: lc.Outputs}}},
	{Prefix: "/api/v1/artifacts", Changes: []Template{{Kind: lc.Outputs}, {Kind: lc.Documents}}},

	// Chats. A turn (new_chat, resume, regenerate) runs tools that may make
	// notes, documents and Studio outputs; its own start and end are told
	// by the chat itself while it runs (the turn flag), this is the close.
	{Prefix: "/api/v1/threads", Changes: []Template{{Kind: lc.Chats}}},
	{Prefix: "/api/v1/threads/{thread_id}", Changes: []Template{{Kind: lc.Chats, IDFrom: "thread_id"}}},
	{Prefix: "/api/v1/new_chat", Changes: turnChanges("")},
	{Prefix: "/api/v1/threads/{thread_id}/resume", Changes: turnChanges("thread_id")},
	{Prefix: "/api/v1/threads/{thread_id}/regenerate", Changes: turnChanges("thread_id")},
	// Undoing an agent's action undoes what it did to a document or a note.
	{Prefix: "/api/v1/threads/{thread_id}/revert", Changes: turnChanges("thread_id")},
	{Prefix: "/api/v1/threads/{thread_id}/revert-turn", Changes: turnChanges("thread_id")},

	// The inbox, and mail drafts waiting for a yes (each has an inbox row).
	{Prefix: "/api/v1/notifications", Changes: []Template{{Kind: lc.Inbox}}},
	{Prefix: "/api/v1/mail/drafts", Changes: []Template{{Kind: lc.Inbox}}},

	// Automations and their runs, and the chat's drafts of one (a draft
	// set aside is no longer offered on its card).
	{Prefix: "/api/v1/automations", Changes: []Template{{Kind: lc.Automations}}},
	{Prefix: "/api/v1/automation-drafts", Changes: []Template{{Kind: lc.Automations}}},
	{Prefix: "/api/v1/automations/{automation_id}", Changes: []Template{{Kind: lc.Automations, IDFrom: "automation_id"}}},

	// Connections. Removing one, or asking it to sync, reaches its documents.
	{Prefix: "/api/v1/connectors", Changes: []Template{{Kind: lc.Connectors}}},
	{Prefix: "/api/v1/connectors/{connector_id}", Changes: []Template{
		{Kind: lc.Connectors, IDFrom: "connector_id"}, {Kind: lc.Documents}}},
	{Prefix: "/api/v1/connectors/{connector_id}/nextcloud/documents/{document_id}", Changes: []Template{
		{Kind: lc.Documents, IDFrom: "document_id"}}},
	{Prefix: "/api/v1/google-drive", Changes: []Template{{Kind: lc.Connectors}}},
	{Prefix: "/api/v1/nextcloud/login", Changes: []Template{{Kind: lc.Connectors}}},
	{Prefix: "/api/v1/search-source-connectors", Changes: []Template{{Kind: lc.Connectors}}},
	{Prefix: "/api/v1/search-source-connectors/{connector_id}", Changes: []Template{{Kind: lc.Connectors, IDFrom: "connector_id"}}},
	{Prefix: "/api/v1/search-source-connectors/{connector_id}", Method: http.MethodDelete, Changes: []Template{
		{Kind: lc.Connectors, IDFrom: "connector_id"}, {Kind: lc.Documents}}},
	{Prefix: "/api/v1/search-source-connectors/{connector_id}/index", Changes: []Template{
		{Kind: lc.Connectors, IDFrom: "connector_id"}, {Kind: lc.Documents}}},
}

// turnChanges is what a chat turn may have changed by the time it ends.
func turnChanges(threadParam string) []Template {
	return []Template{
		{Kind: lc.Chats, IDFrom: threadParam},
		{Kind: lc.Notes}, {Kind: lc.Documents}, {Kind: lc.Folders}, {Kind: lc.Outputs},
	}
}
