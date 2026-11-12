import type { APIRequestContext } from "@playwright/test";
import { authHeaders, BACKEND_URL } from "./auth";

/**
 * The notes routes (soloclef_api/internal/notes), as the specs call them:
 * one kind of note, at /api/v1/notes, the signed-in account's own. A note
 * may be filed in a notebook and may be a source of any number of them;
 * neither is a different kind of note.
 */

/** A notebook as a note names it. */
export type NoteNotebookRow = { id: number; title: string; icon: string | null };

/** A note as the API answers it (features/notes/model/note.ts, the fields specs read). */
export type NoteRow = {
	id: number;
	title: string;
	content: string;
	origin: "hand" | "chat" | "automation";
	notebook: NoteNotebookRow | null;
	sources: NoteNotebookRow[];
	pinned: boolean;
	/** "pending" until the indexer has read the newest text; then "ready". */
	index_state: string;
};

export const notesUrl = `${BACKEND_URL}/api/v1/notes`;
export const noteUrl = (id: number) => `${notesUrl}/${id}`;

async function ok<T>(response: Awaited<ReturnType<APIRequestContext["get"]>>, what: string) {
	if (!response.ok()) {
		throw new Error(`${what} failed (${response.status()}): ${await response.text()}`);
	}
	return (await response.json()) as T;
}

export async function getNote(request: APIRequestContext, token: string, id: number) {
	return ok<NoteRow>(await request.get(noteUrl(id), { headers: authHeaders(token) }), "getNote");
}

/** The note's text as the server has it, trimmed (the editor ends a note with its own newline). */
export async function storedText(request: APIRequestContext, token: string, id: number) {
	return (await getNote(request, token, id)).content.trim();
}

/** One notebook's notes, the first page (what its Notes tab lists). */
export async function notesIn(request: APIRequestContext, token: string, notebookId: number) {
	const page = await ok<{ items: { id: number; title: string }[] }>(
		await request.get(`${notesUrl}?notebook_id=${notebookId}&limit=50`, {
			headers: authHeaders(token),
		}),
		"notesIn"
	);
	return page.items;
}

/** What a spec writes a note with: the create route's own fields. */
export type NewNoteFields = { title: string; content: string; notebook_id?: number };

/**
 * Makes notes for one test and deletes them when it ends. Every test signs in
 * as the same account, and a note outlives the notebook it is filed in (the
 * notebook going only unfiles it), so a test that did not delete its notes
 * would leave them in every later test's Notes page.
 */
export class NoteMaker {
	private readonly made: number[] = [];

	constructor(
		private readonly request: APIRequestContext,
		private readonly token: string
	) {}

	async create(fields: NewNoteFields): Promise<NoteRow> {
		const note = await ok<NoteRow>(
			await this.request.post(notesUrl, { headers: authHeaders(this.token), data: fields }),
			"createNote"
		);
		this.made.push(note.id);
		return note;
	}

	/** A note the page created (New note), deleted with the rest. */
	track(id: number) {
		this.made.push(id);
	}

	async cleanUp() {
		for (const id of this.made) {
			const response = await this.request.delete(noteUrl(id), { headers: authHeaders(this.token) });
			// 404 is a note the test deleted itself.
			if (!response.ok() && response.status() !== 404) {
				throw new Error(`deleting note ${id} failed (${response.status()})`);
			}
		}
	}
}

/** The body of a `notes` fixture: a NoteMaker for the test, cleaned up after it. */
export async function provideNotes(
	request: APIRequestContext,
	token: string,
	use: (maker: NoteMaker) => Promise<void>
) {
	const maker = new NoteMaker(request, token);
	try {
		await use(maker);
	} finally {
		await maker.cleanUp();
	}
}
