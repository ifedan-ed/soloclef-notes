import type { APIRequestContext } from "@playwright/test";
import { authHeaders, BACKEND_URL } from "./auth";

export type NotebookRow = {
	id: number;
	workspace_id: number;
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

const nb = (ws: number, id?: number) =>
	`${BACKEND_URL}/api/v1/workspaces/${ws}/notebooks${id === undefined ? "" : `/${id}`}`;

export async function listNotebooks(request: APIRequestContext, token: string, ws: number) {
	return ok<NotebookRow[]>(
		await request.get(nb(ws), { headers: authHeaders(token) }),
		"listNotebooks"
	);
}

export async function createNotebook(
	request: APIRequestContext,
	token: string,
	ws: number,
	title: string,
	icon = "📓"
) {
	return ok<NotebookRow>(
		await request.post(nb(ws), { headers: authHeaders(token), data: { title, icon } }),
		"createNotebook"
	);
}

export async function deleteNotebook(
	request: APIRequestContext,
	token: string,
	ws: number,
	id: number
) {
	const response = await request.delete(nb(ws, id), { headers: authHeaders(token) });
	if (!response.ok() && response.status() !== 404) {
		throw new Error(`deleteNotebook failed (${response.status()})`);
	}
}

export async function listSources(
	request: APIRequestContext,
	token: string,
	ws: number,
	id: number
) {
	return ok<SourceRow[]>(
		await request.get(`${nb(ws, id)}/sources`, { headers: authHeaders(token) }),
		"listSources"
	);
}

export async function listNotes(request: APIRequestContext, token: string, ws: number, id: number) {
	return ok<{ id: number; title: string; content: string }[]>(
		await request.get(`${nb(ws, id)}/notes`, { headers: authHeaders(token) }),
		"listNotes"
	);
}

export async function createNote(
	request: APIRequestContext,
	token: string,
	ws: number,
	id: number,
	title: string,
	content: string
) {
	return ok<{ id: number }>(
		await request.post(`${nb(ws, id)}/notes`, {
			headers: authHeaders(token),
			data: { title, content },
		}),
		"createNote"
	);
}

export async function getNotebook(
	request: APIRequestContext,
	token: string,
	ws: number,
	id: number
) {
	return ok<NotebookRow & Record<string, unknown>>(
		await request.get(nb(ws, id), { headers: authHeaders(token) }),
		"getNotebook"
	);
}

export async function listThreads(
	request: APIRequestContext,
	token: string,
	ws: number,
	id: number
) {
	return ok<{ id: number }[]>(
		await request.get(`${nb(ws, id)}/threads`, { headers: authHeaders(token) }),
		"listThreads"
	);
}

export async function listStudioOutputs(
	request: APIRequestContext,
	token: string,
	ws: number,
	id: number
) {
	return ok<{ id: number; kind: string; status: string; title: string }[]>(
		await request.get(`${nb(ws, id)}/studio/outputs`, { headers: authHeaders(token) }),
		"listStudioOutputs"
	);
}

export type SourceDetail = SourceRow & {
	kind: "document" | "folder";
	target_id: number;
	status: string | null;
};

/**
 * Add a real, indexed source the way a user can: write a note, then
 * "Convert to source". The backend creates a NOTE document and Celery
 * indexes it (fake embeddings), so chunks and citations are real rows.
 */
export async function addNoteSource(
	request: APIRequestContext,
	token: string,
	ws: number,
	id: number,
	title: string,
	content: string
): Promise<SourceDetail> {
	const note = await createNote(request, token, ws, id, title, content);
	const converted = await request.post(`${nb(ws, id)}/notes/${note.id}/convert`, {
		headers: authHeaders(token),
	});
	await ok(converted, "convertNote");
	const deadline = Date.now() + 60_000;
	for (;;) {
		const sources = (await listSources(request, token, ws, id)) as SourceDetail[];
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
	ws: number,
	id: number,
	kind: SeedKind,
	status: "ready" | "running" | "failed" = "ready",
	title?: string,
	content?: { markdown?: string; html?: string }
): Promise<{ output_id: number; artifact_id: number | null }> {
	return ok(
		await request.post(`${BACKEND_URL}/__e2e__/seed/studio-output`, {
			headers: seedHeaders(),
			data: { workspace_id: ws, notebook_id: id, kind, status, title, ...content },
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
	ws: number,
	id: number,
	outputId: number
): Promise<{ output_id: number; artifact_id: number | null }> {
	return ok(
		await request.post(`${BACKEND_URL}/__e2e__/seed/complete-output`, {
			headers: seedHeaders(),
			data: { workspace_id: ws, notebook_id: id, output_id: outputId },
		}),
		"completeOutput"
	);
}
