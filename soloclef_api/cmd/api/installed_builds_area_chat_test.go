package main

import "git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"

func init() { registerArea(chatArea) }

// The chat, as 1.1.0 uses it inside a notebook: the notebook's
// chats, a chat's saved messages, its turn status and the approvals it waits
// on, the action log and its undo, asking (new_chat), asking again
// (regenerate), answering approvals (resume), Stop, the follow-up questions,
// and deleting the notebook's chat history. Ids 5000-5999.
//
// The model proxy is not run here (it is given an address nothing listens
// on), so a turn that reaches it fails inside its stream: the route has
// already answered 200 and the build shows the error event, which is the
// contract the build was compiled against.
var chatArea = contractArea{
	Name:  "chat",
	Order: 50,
	Seed: `
INSERT INTO notebooks (id, user_id, title) VALUES
 (5001, '` + testpostgres.Alice + `', 'Tides'),
 (5002, '` + testpostgres.Alice + `', 'Chats to clear');
INSERT INTO new_chat_threads (id, user_id, title, notebook_id, created_at, updated_at) VALUES
 (5101, '` + testpostgres.Alice + `', 'Tides', 5001, '2026-11-12T08:00:00Z', '2026-11-12T08:05:00Z'),
 (5102, '` + testpostgres.Alice + `', 'Asked anew', 5001, '2026-11-12T09:00:00Z', '2026-11-12T09:00:00Z'),
 (5103, '` + testpostgres.Alice + `', 'To clear', 5002, '2026-11-12T10:00:00Z', '2026-11-12T10:00:00Z');
INSERT INTO new_chat_messages (id, thread_id, role, content, author_id, turn_id, created_at) VALUES
 (5201, 5101, 'USER', '[{"type":"text","text":"What makes the tides?"}]', '` + testpostgres.Alice + `', 'turn-5201', '2026-11-12T08:00:00Z'),
 (5202, 5101, 'ASSISTANT', '[{"type":"text","text":"The Moon''s pull, mostly."}]', NULL, 'turn-5201', '2026-11-12T08:00:30Z'),
 (5203, 5101, 'USER', '[{"type":"text","text":"File my lecture notes."}]', '` + testpostgres.Alice + `', 'turn-5203', '2026-11-12T08:04:00Z'),
 (5204, 5101, 'ASSISTANT', '[{"type":"text","text":"Made two folders."}]', NULL, 'turn-5203', '2026-11-12T08:05:00Z');
-- Two folders the assistant made, each with the action row its undo reads
-- (internal/knowledgebase): one undone alone, one with its whole turn.
INSERT INTO folders (id, user_id, name, position) VALUES
 (5401, '` + testpostgres.Alice + `', 'Lectures', 'a0'),
 (5402, '` + testpostgres.Alice + `', 'Term 2', 'a1');
INSERT INTO agent_action_log (id, thread_id, user_id, tool_name, args, result_id, reversible, reverse_descriptor, tool_call_id, chat_turn_id, created_at) VALUES
 (5301, 5101, '` + testpostgres.Alice + `', 'create_folder', '{"name":"Lectures"}', 'folder:5401', true, '{"folder":{"id":5401,"name":"Lectures","parent_id":null}}', 'call-5301', 'turn-5201', '2026-11-12T08:00:20Z'),
 (5302, 5101, '` + testpostgres.Alice + `', 'create_folder', '{"name":"Term 2"}', 'folder:5402', true, '{"folder":{"id":5402,"name":"Term 2","parent_id":null}}', 'call-5302', 'turn-5203', '2026-11-12T08:04:30Z');`,
	Cases: []contractCase{
		// Opening a notebook's chat (lib/api/endpoints.ts notebookThreads,
		// lib/chat/engine.ts open/reload).
		{Operation: "GET /api/v1/notebooks/{notebook_id}/threads", Path: "/api/v1/notebooks/5001/threads", Status: 200},
		{Operation: "GET /api/v1/threads/{thread_id}", Path: "/api/v1/threads/5101", Status: 200},
		{Operation: "GET /api/v1/threads/{thread_id}/turn-status", Path: "/api/v1/threads/5101/turn-status", Status: 200},
		{Operation: "GET /api/v1/threads/{thread_id}/messages", Path: "/api/v1/threads/5101/messages?skip=0&limit=100", Status: 200},
		{Operation: "GET /api/v1/threads/{thread_id}/pending-interrupts", Path: "/api/v1/threads/5101/pending-interrupts", Status: 200},
		// The action log, one page of the server's largest (lib/chat/actions.ts).
		{Operation: "GET /api/v1/threads/{thread_id}/actions", Path: "/api/v1/threads/5101/actions?page=0&page_size=200", Status: 200},
		// A notebook with no chat gets its first.
		{Operation: "POST /api/v1/notebooks/{notebook_id}/threads", Path: "/api/v1/notebooks/5002/threads", Status: 201},

		// Undo one change, then a whole turn's.
		{Operation: "POST /api/v1/threads/{thread_id}/revert/{action_id}", Path: "/api/v1/threads/5101/revert/5301", Status: 200},
		{Operation: "POST /api/v1/threads/{thread_id}/revert-turn/{chat_turn_id}", Path: "/api/v1/threads/5101/revert-turn/turn-5203", Status: 200},

		// Three follow-up questions under the latest answer: the model is
		// asked, and this test runs none.
		{Operation: "POST /api/v1/notebooks/{notebook_id}/followups", Path: "/api/v1/notebooks/5001/followups",
			Body: `{"answer":"The Moon's pull, mostly."}`, Status: 503, Unavailable: "the model proxy",
			Why: "the model proxy is not run here; the build shows the 503's sentence and keeps the question"},

		// Asking (lib/chat/request.ts newChatBody), in its own chat so the
		// saved one above stays as seeded.
		// The model proxy is checked before the stream opens, so its absence
		// is the 503 MODEL_PROVIDER_UNAVAILABLE the build shows as "The model
		// service cannot be reached right now"; the body was read and
		// accepted to get there.
		{Operation: "POST /api/v1/new_chat", Path: "/api/v1/new_chat", Status: 503, Unavailable: "the model proxy",
			Why:  "the model proxy is not run here; the build shows the 503's sentence and keeps the question",
			Body: `{"chat_id":5102,"user_query":"Why are there two tides a day?","retrieval_scope":"documents","filesystem_mode":"cloud","client_platform":"web","messages":[]}`},
		// Stop: sent once while a turn runs, whoever started it.
		{Operation: "POST /api/v1/threads/{thread_id}/cancel-active-turn", Path: "/api/v1/threads/5102/cancel-active-turn", Status: 200},
		// Asking a question again (regenerateBody), from the saved question.
		{Operation: "POST /api/v1/threads/{thread_id}/regenerate", Path: "/api/v1/threads/5101/regenerate", Status: 503, Unavailable: "the model proxy",
			Why:  "the model proxy is not run here; the build shows the 503's sentence and keeps the question",
			Body: `{"retrieval_scope":"documents","user_query":null,"filesystem_mode":"cloud","client_platform":"web","from_message_id":5201}`},
		// Answering the approvals a paused turn waits on (resumeBody, one
		// decision per card with its tool_call_id: lib/chat/hitl.ts). A paused
		// turn is an agent checkpoint only a model run writes, so none is
		// seeded: the body is read and accepted, and the chat is not waiting.
		// The build resumes only after pending-interrupts listed a card.
		{Operation: "POST /api/v1/threads/{thread_id}/resume", Path: "/api/v1/threads/5101/resume", Status: 409,
			Why:  "no paused turn can be seeded without a model run; 409 NOT_AWAITING_APPROVAL is what the build shows as nothing waiting",
			Body: `{"retrieval_scope":"documents","decisions":[{"type":"approve","tool_call_id":"call-5205"}],"filesystem_mode":"cloud","client_platform":"web"}`},

		// Delete chat history: the notebook's chats this person started.
		{Operation: "DELETE /api/v1/notebooks/{notebook_id}/threads", Path: "/api/v1/notebooks/5002/threads", Status: 204},
	},
}
