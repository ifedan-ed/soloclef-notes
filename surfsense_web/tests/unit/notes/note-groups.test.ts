import assert from "node:assert/strict";
import test from "node:test";
import { groupNotes, hasAutomationNotes } from "@/features/notes/lib/note-groups";
import { type WorkspaceNote, workspaceNoteSchema } from "@/features/notes/model/workspace-note";

let nextId = 1;

function note(overrides: Partial<WorkspaceNote> = {}): WorkspaceNote {
	return workspaceNoteSchema.parse({
		kind: "notebook_note",
		id: nextId++,
		title: "A note",
		preview: "",
		origin: "hand",
		notebook: null,
		created_at: "2026-10-09T12:00:00Z",
		updated_at: "2026-10-09T12:00:00Z",
		...overrides,
	});
}

const email = { id: 18, title: "Email Search Assistance", icon: "📬" };
const iq = { id: 11, title: "IQ, Motivation and Academic Performance" };
const notesFolder = { id: 1, name: "notes", path: ["notes"], tree_order: 2 };
const projects = { id: 10, name: "Projects", path: ["Projects"], tree_order: 0 };
const q4 = { id: 13, name: "Q4", path: ["Projects", "Q4"], tree_order: 1 };
const daily = { id: 3, name: "Daily unread emails summary" };

test("by folder: each notebook's notes, then each folder in tree order, then the loose ones", () => {
	// The server sends notes newest first.
	const summary = note({
		kind: "document",
		origin: "automation",
		folder: notesFolder,
		automation: daily,
	});
	const emailNote = note({ notebook: email });
	const loose = note({ kind: "document", origin: "chat" });
	const plan = note({ kind: "document", folder: q4 });
	const iqNote = note({ notebook: iq });
	const olderEmailNote = note({ notebook: email });
	const brief = note({ kind: "document", folder: projects });

	const groups = groupNotes(
		[summary, emailNote, loose, plan, iqNote, olderEmailNote, brief],
		"folders"
	);

	assert.deepEqual(
		groups.map((group) => [group.kind, group.title, group.notes.map((n) => n.id)]),
		[
			["notebook", "Email Search Assistance", [emailNote.id, olderEmailNote.id]],
			["notebook", "IQ, Motivation and Academic Performance", [iqNote.id]],
			["folder", "Projects", [brief.id]],
			["folder", "Projects / Q4", [plan.id]],
			["folder", "notes", [summary.id]],
			["no_folder", null, [loose.id]],
		]
	);
	assert.equal(groups[0].icon, "📬");
	assert.equal(new Set(groups.map((group) => group.key)).size, groups.length);
});

test("by automation: only automation notes, one group per automation, newest first", () => {
	const deletedDoc = { id: null, name: "Old digest" };
	const latest = note({
		kind: "document",
		origin: "automation",
		folder: notesFolder,
		automation: daily,
	});
	const handNote = note({ notebook: email });
	const saved = note({ origin: "automation", notebook: email, automation: daily });
	const orphanDoc = note({ kind: "document", origin: "automation", automation: deletedDoc });
	// A notebook note whose automation was deleted carries no automation.
	const orphanNote = note({ origin: "automation", notebook: iq, automation: null });

	const groups = groupNotes([latest, handNote, saved, orphanDoc, orphanNote], "automations");

	assert.deepEqual(
		groups.map((group) => [group.kind, group.title, group.notes.map((n) => n.id)]),
		[
			["automation", "Daily unread emails summary", [latest.id, saved.id]],
			["automation", "Old digest", [orphanDoc.id]],
			["automation", null, [orphanNote.id]],
		]
	);
});

test("no notes, no groups; the automation view is offered only when there is one", () => {
	assert.deepEqual(groupNotes([], "folders"), []);
	assert.deepEqual(groupNotes([note({ notebook: email })], "automations"), []);
	assert.equal(hasAutomationNotes([note({ notebook: email })]), false);
	assert.equal(
		hasAutomationNotes([note({ origin: "automation", notebook: email, automation: daily })]),
		true
	);
});

test("a notes list from a backend without folders still parses, every document at the top", () => {
	const row = workspaceNoteSchema.parse({
		kind: "document",
		id: 207,
		title: "Email_Summary_2026-10-09",
		preview: "",
		origin: "automation",
		automation: { id: 3, name: "Daily" },
		created_at: "2026-10-09T12:02:46Z",
		updated_at: "2026-10-09T12:02:46Z",
	});
	assert.equal(row.folder, undefined);
	assert.deepEqual(
		groupNotes([row], "folders").map((group) => group.kind),
		["no_folder"]
	);
});
