package main

import "git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"

func init() { registerArea(libraryArea) }

// The library: a notebook's own row and its sources, notes, documents and
// their versions, folders, uploads, research and the report templates, as
// the installed builds send them (features/notebooks, features/notes,
// features/sources, lib/uploads, lib/api/endpoints.ts at 731477d8a and
// a7ba0c1c3, which send the same requests here). Ids 3000-3999.
//
// Notebooks 3001 (read, edited, its sources), 3002 (deleted), 3003
// (copied), 3004 (pinned and unpinned). Document 3001 has two versions,
// three chunks and a cached source guide; 3002-3003 are further sources;
// 3005 and folder 3302 are added as sources by the cases.
var libraryArea = contractArea{
	Name:  "library",
	Order: 30,
	Seed: `
INSERT INTO notebooks (id, user_id, title, icon) VALUES
 (3001, '` + testpostgres.Alice + `', 'Tides', '🌊'),
 (3002, '` + testpostgres.Alice + `', 'To delete', NULL),
 (3003, '` + testpostgres.Alice + `', 'To copy', NULL),
 (3004, '` + testpostgres.Alice + `', 'To pin', NULL);
INSERT INTO documents (id, user_id, title, document_type, document_metadata, content, content_hash, source_markdown, status) VALUES
 (3001, '` + testpostgres.Alice + `', 'Tide tables', 'FILE', '{"pages": 2}', 'The spring tide peaks after the full moon.', 'h3001', '# Tides', '{"state": "ready"}'),
 (3002, '` + testpostgres.Alice + `', 'Moon notes', 'FILE', '{}', 'The moon pulls the sea.', 'h3002', '# Moon', '{"state": "ready"}'),
 (3003, '` + testpostgres.Alice + `', 'Shore notes', 'FILE', '{}', 'A limpet returns to its scar.', 'h3003', '# Shore', '{"state": "ready"}'),
 (3005, '` + testpostgres.Alice + `', 'Harbour log', 'FILE', '{}', 'High water at six.', 'h3005', '# Harbour', '{"state": "ready"}');
INSERT INTO document_versions (document_id, version_number, source_markdown, content_hash, title, created_at) VALUES
 (3001, 1, '# Tides, first', 'h3001-v1', 'Tide tables', '2026-11-01T08:00:00Z'),
 (3001, 2, '# Tides', 'h3001', 'Tide tables', '2026-11-02T08:00:00Z');
INSERT INTO chunks (id, document_id, user_id, content, "position") VALUES
 (3201, 3001, '` + testpostgres.Alice + `', 'The spring tide peaks after the full moon.', 0),
 (3202, 3001, '` + testpostgres.Alice + `', 'The neap tide follows the quarter moon.', 1),
 (3203, 3001, '` + testpostgres.Alice + `', 'A limpet returns to its scar.', 2);
INSERT INTO folders (id, user_id, name, position) VALUES
 (3301, '` + testpostgres.Alice + `', 'Charts', 'a0'),
 (3302, '` + testpostgres.Alice + `', 'Logs', 'a1');
INSERT INTO notebook_sources (id, notebook_id, user_id, document_id, folder_id) VALUES
 (3401, 3001, '` + testpostgres.Alice + `', 3001, NULL),
 (3402, 3001, '` + testpostgres.Alice + `', 3002, NULL),
 (3403, 3001, '` + testpostgres.Alice + `', 3003, NULL),
 (3404, 3001, '` + testpostgres.Alice + `', NULL, 3301);
INSERT INTO document_source_guides (document_id, content_hash, summary) VALUES
 (3001, 'h3001', 'Tide tables for the harbour, with the spring and neap tides.');`,
	Cases: []contractCase{
		// The Notebooks home, a notebook, its sources (lib/api/endpoints.ts).
		{Operation: "GET /api/v1/notebooks", Path: "/api/v1/notebooks", Status: 200},
		{Operation: "GET /api/v1/notebooks/{notebook_id}", Path: "/api/v1/notebooks/3001", Status: 200},
		{Operation: "GET /api/v1/notebooks/{notebook_id}/sources", Path: "/api/v1/notebooks/3001/sources", Status: 200},
		// "Found inside notebooks" (features/notebooks/search: 8 notebooks).
		// The passages are found by meaning: the query is embedded through
		// the model proxy and looked up in Qdrant, neither of which this
		// test runs, so the route says search is unavailable (the app shows
		// its retry).
		{Operation: "GET /api/v1/notebooks/search", Path: "/api/v1/notebooks/search?q=tide&limit=8", Status: 503,
			Unavailable: "the model proxy and Qdrant", Why: "the search embeds the query and asks Qdrant; the app shows Try again"},
		// The citation sheet: the cited chunk with 5 on each side, and its
		// source's guide (cached here; a guide not yet written asks the model).
		{Operation: "GET /api/v1/chunks/{chunk_id}/document", Path: "/api/v1/chunks/3202/document?chunk_window=5", Status: 200},
		{Operation: "GET /api/v1/notebooks/{notebook_id}/sources/{source_id}/guide", Path: "/api/v1/notebooks/3001/sources/3401/guide", Status: 200},
		// A document opened from Sources: how to show it, its text, its
		// versions (features/sources/api.ts).
		{Operation: "GET /api/v1/documents/{document_id}/view-manifest", Path: "/api/v1/documents/3001/view-manifest", Status: 200},
		{Operation: "GET /api/v1/documents/{document_id}/editor-content", Path: "/api/v1/documents/3001/editor-content", Status: 200},
		{Operation: "GET /api/v1/documents/{document_id}/versions", Path: "/api/v1/documents/3001/versions", Status: 200},
		{Operation: "GET /api/v1/documents/{document_id}/versions/{version_number}", Path: "/api/v1/documents/3001/versions/1", Status: 200},
		// The Add sources pages: the parser choice, the library's title search
		// (an empty term is sent as title=, page 0) and the folders; the
		// automation builder's mention list sends no title and page 1.
		{Operation: "GET /api/v1/etl/capabilities", Path: "/api/v1/etl/capabilities", Status: 200},
		{Operation: "GET /api/v1/documents/search/titles", Path: "/api/v1/documents/search/titles?title=&page=0&page_size=20", Status: 200},
		{Operation: "GET /api/v1/documents/search/titles", Path: "/api/v1/documents/search/titles?title=Tide&page=0&page_size=20", Status: 200},
		{Operation: "GET /api/v1/documents/search/titles", Path: "/api/v1/documents/search/titles?page=1&page_size=20", Status: 200},
		{Operation: "GET /api/v1/folders", Path: "/api/v1/folders", Status: 200},
		// Research: which sources this server offers, then a search. The
		// search names Exa, which this account has not connected, so the
		// stream says so without the server reaching the internet (PubMed,
		// the app's default, is a public service this test must not call).
		{Operation: "GET /api/v1/notebooks/{notebook_id}/research/sources", Path: "/api/v1/notebooks/3001/research/sources", Status: 200},
		{Operation: "POST /api/v1/notebooks/{notebook_id}/research/search", Path: "/api/v1/notebooks/3001/research/search",
			Body: `{"query":"spring tides","source":"exa","mode":"fast"}`, Status: 200},
		{Operation: "POST /api/v1/notebooks/{notebook_id}/research/search", Path: "/api/v1/notebooks/3001/research/search",
			Body: `{"query":"spring tides","source":"exa","sources":["exa","tavily"],"mode":"deep"}`, Status: 200},
		// "Read a web page": a public page would be fetched from the
		// internet, which this test must not reach, so the request names a
		// private address and the route refuses it before fetching, with the
		// sentence the app shows under the box.
		{Operation: "POST /api/v1/notebooks/{notebook_id}/research/read-page", Path: "/api/v1/notebooks/3001/research/read-page",
			Body: `{"url":"http://127.0.0.1:9/tides"}`, Status: 422,
			Why: "a private address is not read (the test has no internet); the app shows the server's sentence under the box"},
		{Operation: "POST /api/v1/notebooks/{notebook_id}/research/import", Path: "/api/v1/notebooks/3001/research/import",
			Body: `{"items":[{"url":"https://example.org/tides","title":"Tides","snippet":"The spring tide peaks after the full moon.","source":"exa"}]}`, Status: 200},
		// The report templates (Studio's Report sheet).
		{Operation: "GET /api/v1/notebook-report-templates", Path: "/api/v1/notebook-report-templates", Status: 200},
		{Operation: "PUT /api/v1/notebook-report-templates/{template_key}", Path: "/api/v1/notebook-report-templates/briefing_doc",
			Body: `{"title":"Briefing doc","description":"A briefing","prompt":"Write a short briefing on the tides."}`, Status: 200},
		{Operation: "DELETE /api/v1/notebook-report-templates/{template_key}", Path: "/api/v1/notebook-report-templates/briefing_doc", Status: 200},

		// A notebook's … menu (features/notebooks/notebookActions.ts): new,
		// rename, emoji, chat settings, pin, copy, delete.
		{Operation: "POST /api/v1/notebooks", Path: "/api/v1/notebooks", Body: `{"title":"Untitled notebook","icon":"📘"}`, Status: 201},
		{Operation: "PATCH /api/v1/notebooks/{notebook_id}", Path: "/api/v1/notebooks/3001", Body: `{"title":"Tides and the moon"}`, Status: 200},
		{Operation: "PATCH /api/v1/notebooks/{notebook_id}", Path: "/api/v1/notebooks/3001", Body: `{"icon":"🌙"}`, Status: 200},
		{Operation: "PATCH /api/v1/notebooks/{notebook_id}", Path: "/api/v1/notebooks/3001",
			Body: `{"chat_goal":"custom","chat_instructions":"Answer briefly.","response_length":"shorter"}`, Status: 200},
		{Operation: "PUT /api/v1/notebooks/{notebook_id}/feature", Path: "/api/v1/notebooks/3004/feature", Status: 200},
		{Operation: "DELETE /api/v1/notebooks/{notebook_id}/feature", Path: "/api/v1/notebooks/3004/feature", Status: 200},
		{Operation: "POST /api/v1/notebooks/{notebook_id}/copy", Path: "/api/v1/notebooks/3003/copy", Body: `{"title":"To copy (copy)"}`, Status: 201},
		{Operation: "DELETE /api/v1/notebooks/{notebook_id}", Path: "/api/v1/notebooks/3002", Status: 204},

		// Sources: add a document and a folder (the Library page, and the
		// upload queue's linking), switch all off, one on, remove one.
		{Operation: "POST /api/v1/notebooks/{notebook_id}/sources", Path: "/api/v1/notebooks/3001/sources",
			Body: `{"targets":[{"kind":"document","id":3005}]}`, Status: 200},
		{Operation: "POST /api/v1/notebooks/{notebook_id}/sources", Path: "/api/v1/notebooks/3001/sources",
			Body: `{"targets":[{"kind":"folder","id":3302}]}`, Status: 200},
		{Operation: "POST /api/v1/notebooks/{notebook_id}/sources/toggle", Path: "/api/v1/notebooks/3001/sources/toggle", Body: `{"enabled":false}`, Status: 200},
		{Operation: "PATCH /api/v1/notebooks/{notebook_id}/sources/{source_id}", Path: "/api/v1/notebooks/3001/sources/3401", Body: `{"enabled":true}`, Status: 200},
		{Operation: "DELETE /api/v1/notebooks/{notebook_id}/sources/{source_id}", Path: "/api/v1/notebooks/3001/sources/3403", Status: 204},

		// A document's versions: restore the first.
		{Operation: "POST /api/v1/documents/{document_id}/versions/{version_number}/restore", Path: "/api/v1/documents/3001/versions/1/restore", Status: 200},

		// Uploads (lib/uploads/uploadQueue.ts): the fields first, then the
		// file as "files", multipart, as expo-file-system's upload task sends
		// them. A file, then a folder's file.
		{Operation: "POST /api/v1/documents/fileupload", Path: "/api/v1/documents/fileupload",
			Upload: &upload{Fields: map[string]string{"use_vision_llm": "false"}, Files: map[string]string{"files": "tides.txt"}},
			Status: 200, Capture: map[string]string{"uploaded": "document_ids.0"}},
		{Operation: "POST /api/v1/documents/fileupload", Path: "/api/v1/documents/fileupload",
			Upload: &upload{Fields: map[string]string{"use_vision_llm": "false", "parser": "local"}, Files: map[string]string{"files": "moon.txt"}},
			Status: 200},
		{Operation: "POST /api/v1/documents/folder-upload", Path: "/api/v1/documents/folder-upload",
			Upload: &upload{Fields: map[string]string{"use_vision_llm": "false", "folder_name": "Harbour", "relative_paths": `["Harbour/log.txt"]`},
				Files: map[string]string{"files": "log.txt"}},
			Status: 200},
		// The next file of the same folder names its root folder (the app
		// sends the one the first file made; an upload's fields are not
		// filled from a capture, so a seeded folder of hers stands in).
		{Operation: "POST /api/v1/documents/folder-upload", Path: "/api/v1/documents/folder-upload",
			Upload: &upload{Fields: map[string]string{"use_vision_llm": "false", "folder_name": "Harbour", "relative_paths": `["Harbour/tides.txt"]`, "root_folder_id": "3301"},
				Files: map[string]string{"files": "tides.txt"}},
			Status: 200},
		// The original of what was just uploaded (Download on a document).
		{Operation: "GET /api/v1/documents/{document_id}/download-original", Path: "/api/v1/documents/{{uploaded}}/download-original", Status: 200},

		// Notes (features/notes): the Notes tab (a page of 50, and its
		// search), a notebook's notes, a new note in a notebook and one in
		// none, then filing, the source switch, pin and delete.
		// The snapshot declares 200 and the server answers 201 Created; the
		// app's client takes any 2xx as success (lib/api/client.ts).
		{Operation: "POST /api/v1/notes", Path: "/api/v1/notes", Body: `{"title":"New note","content":"","notebook_id":3001}`, Status: 201,
			Capture: map[string]string{"note": "id"}},
		{Operation: "POST /api/v1/notes", Path: "/api/v1/notes", Body: `{"title":"New note","content":"","notebook_id":null}`, Status: 201},
		{Operation: "GET /api/v1/notes", Path: "/api/v1/notes?limit=50", Status: 200},
		{Operation: "GET /api/v1/notes", Path: "/api/v1/notes?limit=50&q=note", Status: 200},
		{Operation: "GET /api/v1/notes", Path: "/api/v1/notes?notebook_id=3001&limit=50", Status: 200},
		{Operation: "GET /api/v1/notes/{note_id}", Path: "/api/v1/notes/{{note}}", Status: 200},
		// The autosave, as the outbox sends saveRequest (noteApi.ts): with a
		// title, and with a blank title left as it is.
		{Operation: "PATCH /api/v1/notes/{note_id}", Path: "/api/v1/notes/{{note}}", Body: `{"title":"Tides, revised","content":"# Tides, revised\n\nTwice a day."}`, Status: 200},
		{Operation: "PATCH /api/v1/notes/{note_id}", Path: "/api/v1/notes/{{note}}", Body: `{"content":"Only the text changed."}`, Status: 200},
		{Operation: "PUT /api/v1/notes/{note_id}/notebook", Path: "/api/v1/notes/{{note}}/notebook", Body: `{"notebook_id":null}`, Status: 200},
		{Operation: "PUT /api/v1/notes/{note_id}/notebook", Path: "/api/v1/notes/{{note}}/notebook", Body: `{"notebook_id":3001}`, Status: 200},
		{Operation: "PUT /api/v1/notes/{note_id}/sources/{notebook_id}", Path: "/api/v1/notes/{{note}}/sources/3001", Status: 200},
		{Operation: "DELETE /api/v1/notes/{note_id}/sources/{notebook_id}", Path: "/api/v1/notes/{{note}}/sources/3001", Status: 200},
		{Operation: "PUT /api/v1/notes/{note_id}/pin", Path: "/api/v1/notes/{{note}}/pin", Status: 200},
		{Operation: "DELETE /api/v1/notes/{note_id}/pin", Path: "/api/v1/notes/{{note}}/pin", Status: 200},
		{Operation: "DELETE /api/v1/notes/{note_id}", Path: "/api/v1/notes/{{note}}", Status: 204},
	},
}
