import type { APIRequestContext } from "@playwright/test";
import { authHeaders, BACKEND_URL } from "./auth";

export type NotebookRow = {
	id: number;
	title: string;
	icon: string | null;
};

export type SourceRow = {
	id: number;
	title: string;
	enabled: boolean;
};

async function ok<T>(response: Awaited<ReturnType<APIRequestContext["get"]>>, what: string) {
	if (!response.ok()) {
		throw new Error(`${what} failed (${response.status()}): ${await response.text()}`);
	}
	if (response.status() === 204) return undefined as T;
	return (await response.json()) as T;
}

// One kind of note, the account's own, at one address; a notebook is a filter.
const NOTES = `${BACKEND_URL}/api/v1/notes`;

// Every notebook is the signed-in account's: no path names an owner.
const nb = (id?: number) => `${BACKEND_URL}/api/v1/notebooks${id === undefined ? "" : `/${id}`}`;

export async function listNotebooks(request: APIRequestContext, token: string) {
	return ok<NotebookRow[]>(
		await request.get(nb(), { headers: authHeaders(token) }),
		"listNotebooks"
	);
}

export async function createNotebook(
	request: APIRequestContext,
	token: string,
	title: string,
	icon = "📓"
) {
	return ok<NotebookRow>(
		await request.post(nb(), { headers: authHeaders(token), data: { title, icon } }),
		"createNotebook"
	);
}

/**
 * Deletes a notebook a test made, and first the notes filed in it. A note
 * outlives its notebook (the notebook going only unfiles it), and every test
 * signs in as the same account: left alone, the notes that `createNote` and
 * `addNoteSource` write would pile up on every later test's Notes page.
 */
export async function deleteNotebook(request: APIRequestContext, token: string, id: number) {
	const filed = await request.get(`${NOTES}?notebook_id=${id}&limit=100`, {
		headers: authHeaders(token),
	});
	// Not ok: the notebook is already gone, and its notes were unfiled with it.
	if (filed.ok()) {
		const { items } = (await filed.json()) as { items: { id: number }[] };
		for (const note of items) {
			await request.delete(`${NOTES}/${note.id}`, { headers: authHeaders(token) });
		}
	}
	const response = await request.delete(nb(id), { headers: authHeaders(token) });
	if (!response.ok() && response.status() !== 404) {
		throw new Error(`deleteNotebook failed (${response.status()})`);
	}
}

export async function listSources(request: APIRequestContext, token: string, id: number) {
	return ok<SourceRow[]>(
		await request.get(`${nb(id)}/sources`, { headers: authHeaders(token) }),
		"listSources"
	);
}

/** The notes filed in a notebook: what its Notes tab lists (first page). */
export async function listNotes(request: APIRequestContext, token: string, id: number) {
	const page = await ok<{ items: { id: number; title: string; content: string }[] }>(
		await request.get(`${NOTES}?notebook_id=${id}&limit=50`, { headers: authHeaders(token) }),
		"listNotes"
	);
	return page.items;
}

/** Writes a note filed in the notebook (soloclef_api/internal/notes: one kind of note). */
export async function createNote(
	request: APIRequestContext,
	token: string,
	id: number,
	title: string,
	content: string
) {
	return ok<{ id: number }>(
		await request.post(NOTES, {
			headers: authHeaders(token),
			data: { title, content, notebook_id: id },
		}),
		"createNote"
	);
}

export async function getNotebook(request: APIRequestContext, token: string, id: number) {
	return ok<NotebookRow & Record<string, unknown>>(
		await request.get(nb(id), { headers: authHeaders(token) }),
		"getNotebook"
	);
}

export async function listThreads(request: APIRequestContext, token: string, id: number) {
	return ok<{ id: number }[]>(
		await request.get(`${nb(id)}/threads`, { headers: authHeaders(token) }),
		"listThreads"
	);
}

export async function listStudioOutputs(request: APIRequestContext, token: string, id: number) {
	return ok<{ id: number; kind: string; status: string; title: string }[]>(
		await request.get(`${nb(id)}/studio/outputs`, { headers: authHeaders(token) }),
		"listStudioOutputs"
	);
}

export type SourceDetail = SourceRow & {
	kind: "document" | "folder";
	target_id: number;
	status: string | null;
};

/**
 * Add a real, indexed source the way a user can: write a note in the
 * notebook, then switch it on as the notebook's source. The note is a
 * document, so the indexer chunks and embeds it and citations are real rows.
 * (There is no "convert": the source is the note itself, not a copy of it.)
 */
export async function addNoteSource(
	request: APIRequestContext,
	token: string,
	id: number,
	title: string,
	content: string
): Promise<SourceDetail> {
	const note = await createNote(request, token, id, title, content);
	const switched = await request.put(`${NOTES}/${note.id}/sources/${id}`, {
		headers: authHeaders(token),
	});
	await ok(switched, "noteAsSource");
	const deadline = Date.now() + 60_000;
	for (;;) {
		const sources = (await listSources(request, token, id)) as SourceDetail[];
		const source = sources.find((s) => s.title === title);
		if (source && (source.status ?? "ready") === "ready") return source;
		if (Date.now() > deadline) {
			throw new Error(`source ${title} not ready: ${JSON.stringify(source)}`);
		}
		await new Promise((r) => setTimeout(r, 1000));
	}
}

/** Chunk ids of a document (for citations). */
export async function documentChunkIds(
	request: APIRequestContext,
	token: string,
	documentId: number
): Promise<number[]> {
	const deadline = Date.now() + 60_000;
	for (;;) {
		const body = await ok<{ items: { id: number }[] }>(
			await request.get(`${BACKEND_URL}/api/v1/documents/${documentId}/chunks?page=0&page_size=5`, {
				headers: authHeaders(token),
			}),
			"documentChunks"
		);
		if (body.items.length) return body.items.map((c) => c.id);
		if (Date.now() > deadline) throw new Error(`document ${documentId} has no chunks`);
		await new Promise((r) => setTimeout(r, 1000));
	}
}

export type SeedKind =
	| "audio"
	| "video"
	| "report"
	| "report_html"
	| "report_docx"
	| "slides"
	| "mindmap"
	| "flashcards"
	| "quiz"
	| "data_table"
	| "infographic";

/** A finished (or running/failed) Studio output, via the test-only seed route. */
export async function seedOutput(
	request: APIRequestContext,
	id: number,
	kind: SeedKind,
	status: "ready" | "running" | "failed" = "ready",
	title?: string,
	content?: { markdown?: string; html?: string }
): Promise<{ output_id: number; artifact_id: number | null }> {
	return ok(
		await request.post(`${BACKEND_URL}/__e2e__/seed/studio-output`, {
			headers: seedHeaders(),
			data: { notebook_id: id, kind, status, title, ...content },
		}),
		"seedOutput"
	);
}

function seedHeaders() {
	return {
		"Content-Type": "application/json",
		"X-E2E-Mint-Secret": process.env.E2E_MINT_SECRET ?? "",
		// The request context carries the session cookie, so the
		// backend's CSRF check wants a same-site Origin.
		Origin: process.env.PLAYWRIGHT_BASE_URL ?? BACKEND_URL,
	};
}

/**
 * Finish a generation the app queued (e.g. from a report suggestion's Add):
 * the test-only route saves that kind's seeded files and marks it ready.
 */
export async function completeOutput(
	request: APIRequestContext,
	id: number,
	outputId: number
): Promise<{ output_id: number; artifact_id: number | null }> {
	return ok(
		await request.post(`${BACKEND_URL}/__e2e__/seed/complete-output`, {
			headers: seedHeaders(),
			data: { notebook_id: id, output_id: outputId },
		}),
		"completeOutput"
	);
}
