import { baseApiService } from "@/lib/apis/base-api.service";
import { type NotesFilter, notesQueryString } from "../lib/notes-filter";
import { noteSchema, notesPageSchema } from "../model/note";

const base = "/api/v1/notes";
const one = (noteId: number) => `${base}/${noteId}`;

/** What a new note starts with. */
export interface NewNote {
	title?: string;
	content?: string;
	/** File it in this notebook. A kept answer is filed in its chat's notebook unless this says otherwise. */
	notebook_id?: number | null;
	/** The answer it is kept from. */
	thread_id?: number;
	message_id?: number;
}

/** A save: whichever of the two changed. */
export interface NoteChanges {
	title?: string;
	content?: string;
}

/** The notes routes (soloclef_api/internal/notes). Every write answers the note as saved. */
export const notesApi = {
	list: (filter: NotesFilter, offset = 0) =>
		baseApiService.get(`${base}?${notesQueryString(filter, offset)}`, notesPageSchema),
	get: (noteId: number) => baseApiService.get(one(noteId), noteSchema),
	create: (body: NewNote) => baseApiService.post(base, noteSchema, { body }),
	update: (noteId: number, body: NoteChanges) =>
		baseApiService.patch(one(noteId), noteSchema, { body }),
	remove: (noteId: number) => baseApiService.delete(one(noteId)),
	setPinned: (noteId: number, pinned: boolean) =>
		pinned
			? baseApiService.put(`${one(noteId)}/pin`, noteSchema)
			: baseApiService.delete(`${one(noteId)}/pin`, noteSchema),
	/** Files the note in a notebook, or in none with null. */
	file: (noteId: number, notebookId: number | null) =>
		baseApiService.put(`${one(noteId)}/notebook`, noteSchema, {
			body: { notebook_id: notebookId },
		}),
	/** The source switch: whether the notebook's chat reads the note. */
	setSource: (noteId: number, notebookId: number, on: boolean) =>
		on
			? baseApiService.put(`${one(noteId)}/sources/${notebookId}`, noteSchema)
			: baseApiService.delete(`${one(noteId)}/sources/${notebookId}`, noteSchema),
};
