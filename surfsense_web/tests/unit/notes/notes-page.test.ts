import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { notebookNoteSchema } from "@/features/notebooks/model/notebook";
import {
	documentNoteActions,
	noteInNotebookHref,
	notesPath,
	notesQuery,
	otherNotebooks,
} from "@/features/notes/lib/note-places";
import { isUnsavedEdit } from "@/features/notes/lib/unsaved-edit";
import { type WorkspaceNote, workspaceNoteSchema } from "@/features/notes/model/workspace-note";

const root = process.cwd();

function note(overrides: Partial<WorkspaceNote> = {}): WorkspaceNote {
	return workspaceNoteSchema.parse({
		kind: "notebook_note",
		id: 9,
		title: "Daily unread emails summary — 2026-10-09",
		preview: "Two unread emails.",
		origin: "automation",
		notebook: { id: 18, title: "Email", icon: "📬" },
		automation: { id: 3, name: "Daily unread emails summary" },
		created_at: "2026-10-09T12:02:46Z",
		updated_at: "2026-10-09T12:02:46Z",
		...overrides,
	});
}

test("a note document the assistant wrote parses with no notebook and no sources", () => {
	const row = workspaceNoteSchema.parse({
		kind: "document",
		id: 207,
		title: "Email_Summary_2026-10-09",
		preview: "Unread",
		origin: "automation",
		notebook: null,
		automation: { id: null, name: "Deleted automation" },
		created_at: "2026-10-09T12:02:46Z",
		updated_at: "2026-10-09T12:02:46Z",
	});
	assert.deepEqual(row.source_in, []);
	assert.equal(row.automation?.id, null);
});

test("a notebook note opens in its notebook's Studio panel; a note document has nowhere to open", () => {
	assert.equal(noteInNotebookHref(1, note()), "/dashboard/1/notebooks/18?note=9");
	assert.equal(noteInNotebookHref(1, note({ kind: "document", notebook: null })), null);
	assert.equal(notesPath(1), "/dashboard/1/notes");
});

test("a note document moves into a notebook only for a member who may delete documents", () => {
	const loose = note({ kind: "document", notebook: null });
	const notebooks = [{ id: 18 }, { id: 11 }];
	assert.deepEqual(documentNoteActions(loose, notebooks, { canDeleteDocuments: true }), {
		addToNotebook: "move",
		canDelete: true,
		sourceTargets: notebooks,
	});
	// An Editor ("Documents (no delete)"): a copy, and no Delete.
	assert.deepEqual(documentNoteActions(loose, notebooks, { canDeleteDocuments: false }), {
		addToNotebook: "copy",
		canDelete: false,
		sourceTargets: notebooks,
	});
});

test("a note document a notebook uses as a source is copied, and is not offered that notebook again", () => {
	const source = note({
		kind: "document",
		notebook: null,
		source_in: [{ id: 18, title: "Email" }],
	});
	const actions = documentNoteActions(source, [{ id: 18 }, { id: 11 }], {
		canDeleteDocuments: true,
	});
	assert.equal(actions.addToNotebook, "copy");
	assert.deepEqual(actions.sourceTargets, [{ id: 11 }]);
});

test("the note document's page renders what documentNoteActions decides", () => {
	const detail = readFileSync(join(root, "features/notes/ui/document-note-detail.tsx"), "utf8");
	assert.match(
		detail,
		/documentNoteActions\(note, notebooks, \{\s*canDeleteDocuments: canPerform\(access, "documents:delete"\)/
	);
	assert.match(detail, /\{actions\.canDelete \? \(\s*<Button[\s\S]*?tn\("delete"\)/);
	assert.match(detail, /picking === "source" \? actions\.sourceTargets : notebooks/);
	assert.match(detail, /actions\.addToNotebook === "move" \? "added_to" : "copied_to"/);
});

test("a note is copied only into the other notebooks", () => {
	const notebooks = [{ id: 18 }, { id: 11 }];
	assert.deepEqual(otherNotebooks(notebooks, note()), [{ id: 11 }]);
	assert.deepEqual(
		otherNotebooks(notebooks, note({ kind: "document", notebook: null })),
		notebooks
	);
});

test("the search sent is trimmed with its spaces collapsed", () => {
	assert.equal(notesQuery("  unread   email "), "unread email");
	assert.equal(notesQuery("   "), "");
});

test("only a real edit to a note document counts as unsaved", () => {
	assert.equal(isUnsavedEdit(null, "# Hi"), false);
	assert.equal(isUnsavedEdit("# Hi\n\n", "# Hi"), false);
	assert.equal(isUnsavedEdit("# Hi  \nthere", "# Hi\nthere"), false);
	assert.equal(isUnsavedEdit("# Hi there", "# Hi"), true);
	assert.equal(isUnsavedEdit("# Hi", undefined), false);
});

test("notes from a backend without origins read as written by hand", () => {
	const parsed = notebookNoteSchema.parse({
		id: 1,
		notebook_id: 2,
		title: "Ideas",
		content: "",
		converted_document_id: null,
		created_at: "2026-10-01T00:00:00Z",
		updated_at: "2026-10-01T00:00:00Z",
	});
	assert.equal(parsed.origin, "hand");
});

test("the rail has Notes after Home, and the Notes route exists", () => {
	const provider = readFileSync(
		join(root, "components/layout/providers/LayoutDataProvider.tsx"),
		"utf8"
	);
	const home = provider.indexOf('title: tSidebar("home")');
	const notes = provider.indexOf('title: tSidebar("notes")');
	assert.ok(home > -1 && notes > home);
	assert.match(provider, /url: notesPath\(workspaceId\)/);
	assert.ok(existsSync(join(root, "app/dashboard/[workspace_id]/notes/page.tsx")));
});

test("Save to note under a chat answer marks the note as kept from a chat", () => {
	const workspace = readFileSync(
		join(root, "features/notebooks/ui/workspace/notebook-workspace.tsx"),
		"utf8"
	);
	assert.match(workspace, /createNote\([\s\S]*?origin: "chat"/);
});

test("the Notes page lists its groups, and offers Automation notes only once there is one", () => {
	const page = readFileSync(join(root, "features/notes/ui/notes-page.tsx"), "utf8");
	assert.match(page, /const groups = groupNotes\(list, view\)/);
	assert.match(page, /const offerViews = view === "automations" \|\| hasAutomationNotes\(list\)/);
	assert.match(page, /\{offerViews \? \(\s*<SegmentedControl/);
	// A note moved into a notebook leaves its folder.
	assert.match(page, /notebook: \{ id: notebook\.id[^}]*\},\s*folder: null,/);
});
