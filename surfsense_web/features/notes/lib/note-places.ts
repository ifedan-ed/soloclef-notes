import { FOCUS_NOTE_PARAM } from "@/features/notebooks/lib/content-search";
import { notebookPath } from "@/lib/workspace-home";
import type { WorkspaceNote } from "../model/workspace-note";

/** Quiet time after the last keystroke before the Notes page asks the server. */
export const NOTES_SEARCH_DEBOUNCE_MS = 300;

/** The Notes page of a workspace. */
export function notesPath(workspaceId: number | string): string {
	return `/dashboard/${workspaceId}/notes`;
}

/** The query worth sending: trimmed, inner spaces collapsed. */
export function notesQuery(raw: string): string {
	return raw.trim().replace(/\s+/g, " ");
}

/** The note open in its notebook's Studio panel; ``null`` for a note no notebook shows. */
export function noteInNotebookHref(workspaceId: number, note: WorkspaceNote): string | null {
	if (note.kind !== "notebook_note" || !note.notebook) return null;
	return `${notebookPath(workspaceId, note.notebook.id)}?${FOCUS_NOTE_PARAM}=${note.id}`;
}

export type DocumentNoteActions<T> = {
	/** "move": it becomes the chosen notebook's note and leaves this list.
	 *  "copy": the file stays too, because a notebook uses it as a source or
	 *  the member may not delete documents (the server's rule, moving.py). */
	addToNotebook: "move" | "copy";
	/** Deleting a note document needs documents:delete (Owners by default). */
	canDelete: boolean;
	/** Notebooks it can still become a source of. */
	sourceTargets: T[];
};

/** What a note that is not in a notebook offers, for this member. */
export function documentNoteActions<T extends { id: number }>(
	note: WorkspaceNote,
	notebooks: T[],
	access: { canDeleteDocuments: boolean }
): DocumentNoteActions<T> {
	const isSource = (id: number) => note.source_in.some((source) => source.id === id);
	return {
		addToNotebook: access.canDeleteDocuments && note.source_in.length === 0 ? "move" : "copy",
		canDelete: access.canDeleteDocuments,
		sourceTargets: notebooks.filter((notebook) => !isSource(notebook.id)),
	};
}

/** Notebooks a note can be added to: all but the one it already lives in. */
export function otherNotebooks<T extends { id: number }>(notebooks: T[], note: WorkspaceNote): T[] {
	return notebooks.filter((notebook) => notebook.id !== note.notebook?.id);
}
