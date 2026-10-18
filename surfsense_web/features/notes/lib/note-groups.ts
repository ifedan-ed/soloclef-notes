import type { WorkspaceNote } from "../model/workspace-note";

/** How the Notes page groups its list. */
export type NotesView = "folders" | "automations";

export type NoteGroup = {
	key: string;
	kind: "notebook" | "folder" | "no_folder" | "automation";
	/** The notebook's title, the folder's path, or the automation's name.
	 *  ``null`` for the notes in no folder and for a deleted automation's notes,
	 *  which the page names itself. */
	title: string | null;
	/** A notebook's own emoji. */
	icon?: string | null;
	/** In the order the server sent them: newest first. */
	notes: WorkspaceNote[];
};

const FOLDER_PATH_SEPARATOR = " / ";

/**
 * The page's groups, for ``notes`` as the server sends them (newest first).
 *
 * - ``folders``: each notebook's notes under the notebook, the notebook with
 *   the newest note first; then the note documents under their folder, in the
 *   documents tree's order; then the ones at the top of the tree.
 * - ``automations``: the notes automations made, one group per automation,
 *   the one with the newest note first.
 *
 * Groups with no notes are left out.
 */
export function groupNotes(notes: WorkspaceNote[], view: NotesView): NoteGroup[] {
	return view === "automations" ? byAutomation(notes) : byFolder(notes);
}

/** Whether the "Automation notes" view has anything to show. */
export function hasAutomationNotes(notes: WorkspaceNote[]): boolean {
	return notes.some((note) => note.origin === "automation");
}

function byFolder(notes: WorkspaceNote[]): NoteGroup[] {
	const notebooks = new Map<number, NoteGroup>();
	const folders = new Map<number, { group: NoteGroup; order: number }>();
	const loose: NoteGroup = { key: "no-folder", kind: "no_folder", title: null, notes: [] };

	for (const note of notes) {
		if (note.kind === "notebook_note" && note.notebook) {
			const { id, title, icon } = note.notebook;
			let group = notebooks.get(id);
			if (!group) {
				group = { key: `notebook-${id}`, kind: "notebook", title, icon, notes: [] };
				notebooks.set(id, group);
			}
			group.notes.push(note);
		} else if (note.folder) {
			const { id, path, tree_order } = note.folder;
			let entry = folders.get(id);
			if (!entry) {
				entry = {
					group: {
						key: `folder-${id}`,
						kind: "folder",
						title: path.join(FOLDER_PATH_SEPARATOR),
						notes: [],
					},
					order: tree_order,
				};
				folders.set(id, entry);
			}
			entry.group.notes.push(note);
		} else {
			loose.notes.push(note);
		}
	}

	const inTreeOrder = [...folders.values()]
		.sort((a, b) => a.order - b.order)
		.map((entry) => entry.group);
	return [...notebooks.values(), ...inTreeOrder, ...(loose.notes.length ? [loose] : [])];
}

function byAutomation(notes: WorkspaceNote[]): NoteGroup[] {
	const groups = new Map<string, NoteGroup>();
	for (const note of notes) {
		if (note.origin !== "automation") continue;
		const automation = note.automation;
		// A deleted automation's documents still carry the name it had.
		const key =
			automation?.id != null
				? `automation-${automation.id}`
				: automation
					? `automation-name-${automation.name}`
					: "automation-deleted";
		let group = groups.get(key);
		if (!group) {
			group = { key, kind: "automation", title: automation?.name ?? null, notes: [] };
			groups.set(key, group);
		}
		group.notes.push(note);
	}
	return [...groups.values()];
}
