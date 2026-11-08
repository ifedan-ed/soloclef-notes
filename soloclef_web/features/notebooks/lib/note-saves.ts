// ---------------------------------------------------------------------------
// Saving a notebook note
// ---------------------------------------------------------------------------
// A notebook note is cached twice: in its notebook's note list (the Studio
// panel) and on its own (the Notes page). A save updated only the cache of
// the place it was made from, and only when the server answered, so the
// note opened in the other place, or opened again before the answer came,
// showed the text from before, and the next keystroke saved that text over
// the edit. Now:
//   - the text being saved goes into both caches as the save starts;
//   - a read of the notes already on its way is called off, and a read asked
//     for while a save is on its way waits for it (noteSavesSettled), so
//     neither brings the text from before back;
//   - a note's saves go to the server one after another, in the order they
//     were made, and only the newest one's answer is put in the caches.
// ---------------------------------------------------------------------------

import type { QueryClient } from "@tanstack/react-query";
import { notebookKeys } from "../api/notebook-query-keys";
import type { NotebookNote } from "../model/notebook";

export type NoteChanges = { title?: string; content?: string };

export interface NoteRef {
	workspaceId: number;
	notebookId: number;
	noteId: number;
}

type Pending = { notebookId: number; settled: Promise<void> };

/** Saves on their way, by workspace. */
const pending = new Map<number, Set<Pending>>();
/** Each note's last save, which its next one waits for. */
const lastSave = new Map<string, Promise<void>>();
/** Each note's newest save, whose answer is the one the caches take. */
const newestSave = new Map<string, number>();
let saves = 0;

const noop = () => undefined;

function track(workspaceId: number, notebookId: number, settled: Promise<void>) {
	const entry = { notebookId, settled };
	const inWorkspace = pending.get(workspaceId) ?? new Set<Pending>();
	pending.set(workspaceId, inWorkspace);
	inWorkspace.add(entry);
	void settled.then(() => {
		inWorkspace.delete(entry);
		if (inWorkspace.size === 0) pending.delete(workspaceId);
	});
}

/**
 * Resolves once the note saves on their way when it is called have been
 * answered (or failed): those of one notebook, or of the whole workspace.
 * A read of notes waits for this first.
 */
export async function noteSavesSettled(workspaceId: number, notebookId?: number): Promise<void> {
	const saving = [...(pending.get(workspaceId) ?? [])].filter(
		(entry) => notebookId === undefined || entry.notebookId === notebookId
	);
	await Promise.all(saving.map((entry) => entry.settled));
}

/**
 * Saves `changes` to a notebook note with `send` (the PATCH), keeping both
 * of its caches on the newest text (see above). Resolves with the saved
 * note; rejects as `send` does, after which both caches are read again.
 */
export function saveNotebookNote(
	queryClient: QueryClient,
	{ workspaceId, notebookId, noteId }: NoteRef,
	changes: NoteChanges,
	send: (changes: NoteChanges) => Promise<NotebookNote>
): Promise<NotebookNote> {
	const listKey = notebookKeys.notes(workspaceId, notebookId);
	const noteKey = notebookKeys.note(workspaceId, notebookId, noteId);
	const id = `${workspaceId}:${notebookId}:${noteId}`;
	saves += 1;
	const number = saves;
	newestSave.set(id, number);

	// A read already on its way was asked before this save: its answer is the
	// text from before. (The list key starts the note's own key, so both go.)
	// Only reads of a cache that has data are called off: one called off with
	// nothing to fall back on would leave its page waiting with no read.
	void queryClient.cancelQueries({
		queryKey: listKey,
		predicate: (query) => query.state.data !== undefined,
	});
	queryClient.setQueryData<NotebookNote[]>(listKey, (list) =>
		list?.map((note) => (note.id === noteId ? { ...note, ...changes } : note))
	);
	queryClient.setQueryData<NotebookNote>(noteKey, (note) =>
		note ? { ...note, ...changes } : note
	);

	const request = (lastSave.get(id) ?? Promise.resolve()).then(() => send(changes));
	const settled = request.then(noop, noop);
	lastSave.set(id, settled);
	void settled.then(() => {
		if (lastSave.get(id) === settled) lastSave.delete(id);
	});
	track(workspaceId, notebookId, settled);

	const isNewest = () => newestSave.get(id) === number;
	return request.then(
		(saved) => {
			if (isNewest()) {
				newestSave.delete(id);
				queryClient.setQueryData<NotebookNote[]>(listKey, (list) =>
					list ? [saved, ...list.filter((note) => note.id !== noteId)] : list
				);
				queryClient.setQueryData<NotebookNote>(noteKey, saved);
			}
			return saved;
		},
		(error: unknown) => {
			if (isNewest()) newestSave.delete(id);
			// The caches hold a text the server may not have: read it again.
			void queryClient.invalidateQueries({ queryKey: listKey });
			throw error;
		}
	);
}
