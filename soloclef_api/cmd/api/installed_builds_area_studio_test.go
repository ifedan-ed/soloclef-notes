package main

import "git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"

func init() { registerArea(studioArea) }

// studioAlice is the seed's owner, spelled once for the SQL below.
const studioAlice = "'" + testpostgres.Alice + "'"

// The Studio, its artifacts and sharing, as 1.0.0 and 1.1.0 use them: the
// output list and its viewers, generating, a report's suggestions, a deck's
// and a quiz's study state, media links, a running job, the public links and
// what a stranger opening one reads. Ids 4000-4999.
//
// The artifacts' files are rows only: this test cannot put bytes in the
// server's file store (a temporary directory the seed cannot reach), so the
// routes that stream a file, or read a deck or a quiz to check a study write
// against it, meet a file that is not there. Their cases still expect what
// the builds need.
var studioArea = contractArea{
	Name:  "studio",
	Order: 40,
	Seed: `
INSERT INTO notebooks (id, user_id, title) VALUES (4001, ` + studioAlice + `, 'Tides');
INSERT INTO documents (id, user_id, title, document_type, content, content_hash, source_markdown, status) VALUES
 (4010, ` + studioAlice + `, 'Tide tables', 'FILE', 'High water twice a day.', 'h4010', 'High water twice a day.', '{"state": "ready"}'),
 (4020, ` + studioAlice + `, 'Tide report', 'ARTIFACT', '', 'h4020', E'# Tide report\n\n## Rounds\n\nTwice a day.\n\n## Springs\n\nAt new and full moon.', '{"state": "ready"}'),
 (4021, ` + studioAlice + `, 'Tide quiz', 'ARTIFACT', '', 'h4021', NULL, '{"state": "ready"}'),
 (4022, ` + studioAlice + `, 'Tide cards', 'ARTIFACT', '', 'h4022', NULL, '{"state": "ready"}'),
 (4023, ` + studioAlice + `, 'Tide talk', 'ARTIFACT', 'Host: hello', 'h4023', NULL, '{"state": "ready"}'),
 (4024, ` + studioAlice + `, 'Tide deck', 'ARTIFACT', '', 'h4024', NULL, '{"state": "ready"}'),
 (4025, ` + studioAlice + `, 'Tide map', 'ARTIFACT', '- Tides', 'h4025', E'# Tide map\n\n- Tides\n  - Springs', '{"state": "ready"}');
INSERT INTO notebook_sources (id, notebook_id, user_id, document_id) VALUES (4015, 4001, ` + studioAlice + `, 4010);
-- The report carries its suggestions for this generation already, as a
-- report's first open leaves them, so its reading view needs no model.
INSERT INTO artifacts (id, document_id, user_id, format, generation, metadata) VALUES
 (4020, 4020, ` + studioAlice + `, 'pdf', 1, '{"report_view": {"generation": 1, "language": "en", "ready": true, "suggestions": [
   {"id": "g1s1", "section": 0, "heading": "Rounds", "kind": "audio", "title": "Rounds, talked through", "focus": "Twice a day.", "state": "open", "output_id": null, "attempts": 0},
   {"id": "g1s2", "section": 1, "heading": "Springs", "kind": "flashcards", "title": "Spring tides", "focus": "New and full moon.", "state": "open", "output_id": null, "attempts": 0}]}}'),
 (4021, 4021, ` + studioAlice + `, 'quiz', 1, NULL),
 (4022, 4022, ` + studioAlice + `, 'flashcards', 1, NULL),
 (4023, 4023, ` + studioAlice + `, 'podcast', 1, '{"duration_seconds": 3}'),
 (4024, 4024, ` + studioAlice + `, 'slides', 1, NULL),
 (4025, 4025, ` + studioAlice + `, 'mindmap', 1, NULL);
INSERT INTO artifact_files (id, artifact_id, role, storage_backend, storage_key, original_filename, mime_type, size_bytes, checksum_sha256) VALUES
 (4030, 4020, 'primary', 'local', 'artifacts/4020/report.pdf', 'Tide report.pdf', 'application/pdf', 33, 'c4030'),
 (4031, 4021, 'primary', 'local', 'artifacts/4021/quiz.json', 'quiz.json', 'application/json', 755, 'c4031'),
 (4032, 4022, 'primary', 'local', 'artifacts/4022/cards.json', 'cards.json', 'application/json', 158, 'c4032'),
 (4033, 4023, 'primary', 'local', 'artifacts/4023/talk.mp3', 'talk.mp3', 'audio/mpeg', 23, 'c4033'),
 (4034, 4024, 'primary', 'local', 'artifacts/4024/deck.pdf', 'deck.pdf', 'application/pdf', 31, 'c4034');
INSERT INTO deliverable_jobs (id, user_id, kind, title, tool_call_id, request, status, phase, progress, artifact_id, heartbeat_at) VALUES
 (4040, ` + studioAlice + `, 'artifact', 'Tide report', 'notebook-studio:4001:c1', '{"kind": "report"}', 'ready', 'done', 100, 4020, NULL),
 (4041, ` + studioAlice + `, 'artifact', 'Tide quiz', 'notebook-studio:4001:c2', '{"kind": "quiz"}', 'ready', 'done', 100, 4021, NULL),
 (4042, ` + studioAlice + `, 'artifact', 'Tide cards', 'notebook-studio:4001:c3', '{"kind": "flashcards"}', 'ready', 'done', 100, 4022, NULL),
 (4044, ` + studioAlice + `, 'video', 'Tide video', 'chat-tool:c5', '{"kind": "video"}', 'running', 'rendering', 40, NULL, now() + interval '1 day'),
 (4045, ` + studioAlice + `, 'artifact', 'Tide deck', 'notebook-studio:4001:c6', '{"kind": "slides"}', 'ready', 'done', 100, 4024, NULL);
INSERT INTO podcast_runs (id, user_id, title, status, artifact_id, duration_seconds) VALUES
 (4043, ` + studioAlice + `, 'Tide talk', 'ready', 4023, 3);
INSERT INTO notebook_outputs (id, notebook_id, user_id, kind, title, options, source_document_ids, deliverable_job_id, podcast_id, generated_by) VALUES
 (4050, 4001, ` + studioAlice + `, 'report', 'Tide report', '{"format": "document", "focus": "Rounds"}', '[4010]', 4040, NULL, '{"model": "contract-model"}'),
 (4051, 4001, ` + studioAlice + `, 'quiz', 'Tide quiz', '{"count": "standard", "difficulty": "medium"}', '[4010]', 4041, NULL, NULL),
 (4052, 4001, ` + studioAlice + `, 'flashcards', 'Tide cards', '{"count": "standard", "difficulty": "medium"}', '[4010]', 4042, NULL, NULL),
 (4053, 4001, ` + studioAlice + `, 'audio', 'Tide talk', '{"format": "deep_dive", "length": "default"}', '[4010]', NULL, 4043, NULL),
 (4055, 4001, ` + studioAlice + `, 'slides', 'Tide deck', '{"format": "detailed", "length": "default"}', '[4010]', 4045, NULL, NULL);
-- What the notebook's chat made, listed by its artifact with no run behind
-- it (migration 00027): the outputs the builds' Studio tab now also meets.
INSERT INTO notebook_outputs (id, notebook_id, user_id, kind, title, options, source_document_ids, artifact_id) VALUES
 (4056, 4001, ` + studioAlice + `, 'mindmap', 'Tide map', '{"kind": "mindmap"}', '[]', 4025);
-- Two public links to the report: one a stranger opens, one its owner
-- revokes from Settings.
INSERT INTO output_public_links (id, user_id, notebook_output_id, share_token, content_hash, snapshot_data, expires_at) VALUES
 (4060, ` + studioAlice + `, 4050, 'contract-share-open', 'h4060', '{
   "kind": "output", "title": "Tide report", "snapshot_at": "2026-11-01T10:00:00+00:00",
   "output": {"title": "Tide report", "kind": "report", "format": "report", "markdown": "# Tide report", "artifact_id": 4020,
     "file": {"filename": "Tide report.pdf", "mime_type": "application/pdf", "size_bytes": 38, "display": "pdf"}},
   "artifact_ids": [4020]}', NULL),
 (4061, ` + studioAlice + `, 4050, 'contract-share-revoked', 'h4061', '{
   "kind": "output", "title": "Tide report",
   "output": {"title": "Tide report", "kind": "report", "format": "report", "markdown": "# Tide report", "artifact_id": 4020, "file": null},
   "artifact_ids": [4020]}', NULL);`,
	// The files the artifact rows point at, so the downloads stream and the
	// study writes read their quiz and deck (sizes match size_bytes above).
	Files: map[string]string{
		"artifacts/4020/report.pdf": "%PDF-1.4\n% contract report\n%%EOF\n",
		"artifacts/4021/quiz.json":  `{"schema_version": 1, "title": "Tide quiz", "questions": [{"question_text": "How often is high tide?", "options": ["Once a day", "Twice a day", "Weekly", "Monthly"], "correct_option_index": 1, "explanation_text": "Two bulges.", "option_rationales": ["No", "Yes", "No", "No", {"question_text": "When are spring tides?", "options": ["New and full moon", "Quarter moons", "Equinox only", "Never"], "correct_option_index": 0, "explanation_text": "Sun and moon align.", "option_rationales": ["Yes", "No", "No", "No"]}]}, {"question_text": "When are spring tides?", "options": ["New and full moon", "Quarter moons", "Equinox only", "Never"], "correct_option_index": 0, "explanation_text": "Sun and moon align.", "option_rationales": ["Yes", "No", "No", "No"]}]}`,
		"artifacts/4022/cards.json": `{"schema_version": 1, "title": "Tide cards", "cards": [{"front_text": "Spring tide", "back_text": "At new and full moon", "hint_text": "Sun and moon align"}]}`,
		"artifacts/4023/talk.mp3":   `ID3contract-audio-bytes`,
		"artifacts/4024/deck.pdf":   "%PDF-1.4\n% contract deck\n%%EOF\n",
	},
	Cases: []contractCase{
		// The Studio tab (lib/api/endpoints.ts: outputs, outputView,
		// reportView, suggestions).
		{Operation: "GET /api/v1/notebooks/{notebook_id}/studio/outputs", Path: "/api/v1/notebooks/4001/studio/outputs", Status: 200},
		{Operation: "GET /api/v1/notebooks/{notebook_id}/studio/outputs/{output_id}/view", Path: "/api/v1/notebooks/4001/studio/outputs/4050/view", Status: 200},
		// An output the notebook's chat made opens as any other.
		{Operation: "GET /api/v1/notebooks/{notebook_id}/studio/outputs/{output_id}/view", Path: "/api/v1/notebooks/4001/studio/outputs/4056/view", Status: 200},
		{Operation: "GET /api/v1/notebooks/{notebook_id}/studio/outputs/{output_id}/report-view", Path: "/api/v1/notebooks/4001/studio/outputs/4050/report-view", Status: 200},
		// The chat's three questions (chatSuggestions) and the Studio
		// dialog's ideas, asked for afresh (suggestions with refresh).
		{Operation: "GET /api/v1/notebooks/{notebook_id}/studio/suggestions", Path: "/api/v1/notebooks/4001/studio/suggestions?kind=chat", Status: 200},
		{Operation: "GET /api/v1/notebooks/{notebook_id}/studio/suggestions", Path: "/api/v1/notebooks/4001/studio/suggestions?kind=report&variant=document&language=English&refresh=true", Status: 200},

		// A report's suggestions: dismiss one, add the other, which starts
		// an output from the report's sources. Starting any output first
		// looks the account's model up in the model proxy's list, which
		// this test does not run, so the add and every generate below
		// answer the route's own 503 ("The model could not be reached
		// right now"); what they answer with a proxy is not checked here.
		{Operation: "POST /api/v1/notebooks/{notebook_id}/studio/outputs/{output_id}/report-suggestions/{suggestion_id}/dismiss", Path: "/api/v1/notebooks/4001/studio/outputs/4050/report-suggestions/g1s1/dismiss", Status: 200},
		{Operation: "POST /api/v1/notebooks/{notebook_id}/studio/outputs/{output_id}/report-suggestions/{suggestion_id}/add", Path: "/api/v1/notebooks/4001/studio/outputs/4050/report-suggestions/g1s2/add",
			Status: 503, Unavailable: "the model proxy (the account's model is looked up in its list)", Why: "the reading view shows the server's sentence and the suggestion stays open"},

		// Generating, as buildStudioRequest writes the body
		// (lib/studio/request.ts, the dialog's defaults): a quiz, a deck
		// with its sources ticked (notebook source ids), and a report.
		{Operation: "POST /api/v1/notebooks/{notebook_id}/studio/generate", Path: "/api/v1/notebooks/4001/studio/generate",
			Body:   `{"kind":"quiz","title":"Tide quiz 2","request_id":"0123456789abcdef0123456789abcde1","count":"standard","difficulty":"medium","focus":"Springs","language":"English"}`,
			Status: 503, Unavailable: "the model proxy (the account's model is looked up in its list)", Why: "the Studio dialog shows the server's sentence"},
		{Operation: "POST /api/v1/notebooks/{notebook_id}/studio/generate", Path: "/api/v1/notebooks/4001/studio/generate",
			Body:   `{"kind":"flashcards","title":"Tide cards 2","request_id":"0123456789abcdef0123456789abcde2","count":"fewer","difficulty":"easy","source_ids":[4015]}`,
			Status: 503, Unavailable: "the model proxy (the account's model is looked up in its list)", Why: "the Studio dialog shows the server's sentence"},
		// A report is drawn in the sandbox, which this test's deployment
		// switches off: the route says so before it asks for a model.
		{Operation: "POST /api/v1/notebooks/{notebook_id}/studio/generate", Path: "/api/v1/notebooks/4001/studio/generate",
			Body:   `{"kind":"report","title":"Tide report 2","request_id":"0123456789abcdef0123456789abcdef","format":"document","file_type":"pdf","focus":"Springs","language":"English"}`,
			Status: 503, Unavailable: "the sandbox (SANDBOX_ENABLED is false in this test)", Why: "the Studio dialog shows the server's sentence"},

		// The artifacts the viewers open: the manifest (what to show), the
		// media links for an image or audio, the file itself.
		{Operation: "GET /api/v1/artifacts/{artifact_id}/manifest", Path: "/api/v1/artifacts/4020/manifest", Status: 200},
		{Operation: "GET /api/v1/artifacts/{artifact_id}/manifest", Path: "/api/v1/artifacts/4021/manifest", Status: 200},
		{Operation: "GET /api/v1/artifacts/{artifact_id}/manifest", Path: "/api/v1/artifacts/4022/manifest", Status: 200},
		{Operation: "POST /api/v1/artifacts/media-links", Path: "/api/v1/artifacts/media-links",
			Body: `{"files":[{"artifact_id":4023,"file_id":4033},{"artifact_id":4020,"file_id":4030}]}`, Status: 200},
		{Operation: "GET /api/v1/artifacts/{artifact_id}/download", Path: "/api/v1/artifacts/4020/download", Status: 200},

		// A deck's and a quiz's study state (lib/api/endpoints.ts and the
		// outbox's studyMutations.ts), generation 1 as the manifest says.
		{Operation: "PATCH /api/v1/artifacts/{artifact_id}/flashcard-progress", Path: "/api/v1/artifacts/4022/flashcard-progress", Body: `{"generation":1,"card_index":0,"mark":"good"}`, Status: 200},
		{Operation: "PUT /api/v1/artifacts/{artifact_id}/flashcard-order", Path: "/api/v1/artifacts/4022/flashcard-order", Body: `{"generation":1,"order":[0]}`, Status: 200},
		{Operation: "DELETE /api/v1/artifacts/{artifact_id}/flashcard-progress", Path: "/api/v1/artifacts/4022/flashcard-progress?generation=1", Status: 200},
		{Operation: "PUT /api/v1/artifacts/{artifact_id}/quiz-answer", Path: "/api/v1/artifacts/4021/quiz-answer", Body: `{"generation":1,"question_index":0,"selected_option_index":1}`, Status: 200},
		// Skip is offered on a question not yet answered: the second.
		{Operation: "PUT /api/v1/artifacts/{artifact_id}/quiz-skip", Path: "/api/v1/artifacts/4021/quiz-skip", Body: `{"generation":1,"question_index":1}`, Status: 200},
		{Operation: "POST /api/v1/artifacts/{artifact_id}/quiz-retake", Path: "/api/v1/artifacts/4021/quiz-retake", Body: `{"generation":1,"mode":"all"}`, Status: 200},

		// A video a chat tool is still making (deliverableJob).
		{Operation: "GET /api/v1/deliverable-jobs/{job_id}", Path: "/api/v1/deliverable-jobs/4044", Status: 200},

		// Sharing an output, then what a stranger holding the link reads,
		// without a credential (features/public).
		{Operation: "POST /api/v1/notebooks/{notebook_id}/studio/outputs/{output_id}/public-link", Path: "/api/v1/notebooks/4001/studio/outputs/4055/public-link", Body: `{"expires_in_days":7}`, Status: 200},
		{Operation: "POST /api/v1/notebooks/{notebook_id}/studio/outputs/{output_id}/public-link", Path: "/api/v1/notebooks/4001/studio/outputs/4053/public-link", Body: `{"expires_in_days":null}`, Status: 200},
		{Operation: "GET /api/v1/public/{share_token}", Path: "/api/v1/public/contract-share-open", Anonymous: true, Status: 200},
		{Operation: "GET /api/v1/public/{share_token}/artifacts/{artifact_id}/content", Path: "/api/v1/public/contract-share-open/artifacts/4020/content", Anonymous: true, Status: 200},
		// A shared chat's podcast card. Only a shared chat carried one, and
		// chats can no longer be shared, so no link a build can open today
		// leads here: the output link's page has no such card. A 404 is the
		// truth, and PublicPodcastCard reads it as no transcript (res.ok is
		// false) and a player that cannot load.
		{Operation: "GET /api/v1/public/{share_token}/podcasts/{podcast_id}", Path: "/api/v1/public/contract-share-open/podcasts/4043", Anonymous: true, Status: 401,
			Why: "only a shared chat had podcast cards and chats are no longer shared; the card treats a failed read as no transcript"},
		{Operation: "GET /api/v1/public/{share_token}/podcasts/{podcast_id}/stream", Path: "/api/v1/public/contract-share-open/podcasts/4043/stream", Anonymous: true, Status: 401,
			Why: "only a shared chat had podcast cards and chats are no longer shared; no output link leads here"},
		// A live notebook link. Every notebook link was removed with
		// migration 00019, so every token names nothing. No route serves the
		// path, so a request without a credential meets the gate's 401
		// (installedbuilds.Retired, gonePage); the updates of 2026-11-14
		// read it as "gone", as a 404 (publicLinkFailure in publicApi.ts).
		{Operation: "GET /api/v1/public-notebooks/{token}", Path: "/api/v1/public-notebooks/contract-notebook-link", Anonymous: true, Status: 401,
			Why: "retired: no notebook link exists any more (installedbuilds.Retired)"},
		{Operation: "GET /api/v1/public-notebooks/{token}/sources/{source_id}", Path: "/api/v1/public-notebooks/contract-notebook-link/sources/4010", Anonymous: true, Status: 401,
			Why: "no notebook link exists any more; reached only from a live page that already said it is gone"},
		{Operation: "GET /api/v1/public-notebooks/{token}/artifacts/{artifact_id}/manifest", Path: "/api/v1/public-notebooks/contract-notebook-link/artifacts/4020/manifest", Anonymous: true, Status: 401,
			Why: "no notebook link exists any more; reached only from a live page that already said it is gone"},
		{Operation: "GET /api/v1/public-notebooks/{token}/artifacts/{artifact_id}/download", Path: "/api/v1/public-notebooks/contract-notebook-link/artifacts/4020/download", Anonymous: true, Status: 401,
			Why: "no notebook link exists any more; reached only from a live page that already said it is gone"},

		// Settings, Public links: both lists, then a revoke.
		{Operation: "GET /api/v1/snapshots", Path: "/api/v1/snapshots", Status: 200},
		// The installed builds read the notebook links beside the output
		// links and show "Couldn't load" when either fails; branch
		// fix/desktop-leftovers-tests answers an empty list
		// (internal/retiredshare).
		{Operation: "GET /api/v1/notebook-links", Path: "/api/v1/notebook-links", Status: 200},
		{Operation: "DELETE /api/v1/snapshots/{snapshot_id}", Path: "/api/v1/snapshots/4061", Status: 200},
		// A notebook link's revoke is offered only for a link the list
		// showed, and the list is always empty now.
		{Operation: "DELETE /api/v1/notebook-links/{link_id}", Path: "/api/v1/notebook-links/4070", Status: 404,
			Why: "offered only for a listed notebook link, and none can be listed; the screen would say it could not revoke a link that does not exist"},

		// The notebook menu's Share: a notebook's live link and a chat's
		// snapshot. Only a Studio output can be shared now; branch
		// fix/desktop-leftovers-tests answers both creates 403 in the error
		// envelope, which the builds show as "This can't be shared publicly."
		// (where a 404 said "try again" for ever).
		// The notebook screen and its menu read the live link. With the
		// route gone the read fails, and the menu reads a failed read as no
		// link ("A live, read-only link"): nothing on screen breaks.
		{Operation: "GET /api/v1/notebooks/{notebook_id}/public-link", Path: "/api/v1/notebooks/4001/public-link", Status: 405,
			Why: "no notebook link exists any more; useNotebookLink's failed read leaves the menu showing no live link (liveOf(undefined) is null)"},
		{Operation: "POST /api/v1/notebooks/{notebook_id}/public-link", Path: "/api/v1/notebooks/4001/public-link", Body: `{"expires_in_days":7,"regenerate":false}`, Status: 403,
			Why: "a notebook can no longer be shared; the menu shows \"This can't be shared publicly.\" for a 403"},
		{Operation: "POST /api/v1/threads/{thread_id}/snapshots", Path: "/api/v1/threads/4080/snapshots", Body: `{"expires_in_days":30}`, Status: 403,
			Why: "a chat can no longer be shared; the menu shows \"This can't be shared publicly.\" for a 403"},
		// Stop sharing is offered only while the menu shows a live link,
		// which it never can now.
		{Operation: "DELETE /api/v1/notebooks/{notebook_id}/public-link", Path: "/api/v1/notebooks/4001/public-link", Status: 405,
			Why: "offered only for a live notebook link, and none exists; nothing is shared, so nothing is left running"},

		// Renaming an output, then deleting it, last: the cases above read it.
		{Operation: "PATCH /api/v1/notebooks/{notebook_id}/studio/outputs/{output_id}", Path: "/api/v1/notebooks/4001/studio/outputs/4055", Body: `{"title":"Tide deck, renamed"}`, Status: 200},
		{Operation: "DELETE /api/v1/notebooks/{notebook_id}/studio/outputs/{output_id}", Path: "/api/v1/notebooks/4001/studio/outputs/4055", Status: 204},
	},
}
