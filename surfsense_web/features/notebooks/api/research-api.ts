import { z } from "zod";
import { baseApiService } from "@/lib/apis/base-api.service";
import { authenticatedFetch } from "@/lib/auth-fetch";
import { buildBackendUrl } from "@/lib/env-config";
import { createSseDataReader, parseResearchEvent } from "../lib/research-sse";
import { notebookSourceSchema } from "../model/notebook";
import {
	type ResearchEvent,
	type ResearchImportItem,
	type ResearchRequest,
	researchSourcesStatusSchema,
} from "../model/research";

const researchPath = (workspaceId: number, notebookId: number) =>
	`/api/v1/workspaces/${workspaceId}/notebooks/${notebookId}/research`;

/** The search endpoint refused the request before streaming anything. */
export class ResearchRequestError extends Error {
	constructor(
		readonly status: number,
		readonly detail: string | null
	) {
		super(detail ?? `Research search failed (${status})`);
		this.name = "ResearchRequestError";
	}
}

async function errorDetail(response: Response): Promise<string | null> {
	try {
		const body = (await response.json()) as { detail?: unknown };
		return typeof body.detail === "string" ? body.detail : null;
	} catch {
		return null;
	}
}

/**
 * Runs one web search for new sources, handing each event over as it
 * streams in. Resolves when the stream closes; abort through ``signal``.
 */
export async function streamResearchSearch({
	workspaceId,
	notebookId,
	request,
	signal,
	onEvent,
}: {
	workspaceId: number;
	notebookId: number;
	request: ResearchRequest;
	signal: AbortSignal;
	onEvent: (event: ResearchEvent) => void;
}): Promise<void> {
	const response = await authenticatedFetch(
		buildBackendUrl(`${researchPath(workspaceId, notebookId)}/search`),
		{
			method: "POST",
			headers: { "Content-Type": "application/json", Accept: "text/event-stream" },
			body: JSON.stringify(request),
			signal,
		}
	);
	if (!response.ok || !response.body) {
		throw new ResearchRequestError(response.status, await errorDetail(response));
	}

	const reader = response.body.getReader();
	const decoder = new TextDecoder();
	const sse = createSseDataReader();
	const emit = (payloads: string[]) => {
		for (const payload of payloads) {
			const event = parseResearchEvent(payload);
			if (event) onEvent(event);
		}
	};
	try {
		while (true) {
			const { done, value } = await reader.read();
			if (done) break;
			emit(sse.push(decoder.decode(value, { stream: true })));
		}
		emit(sse.push(decoder.decode()));
		emit(sse.end());
	} finally {
		reader.releaseLock();
	}
}

export const researchApi = {
	/** Which key-backed sources (Exa, Tavily) this workspace cannot search, and why. */
	sourcesStatus: (workspaceId: number, notebookId: number) =>
		baseApiService.get(
			`${researchPath(workspaceId, notebookId)}/sources`,
			researchSourcesStatusSchema
		),
	/**
	 * Adds one pasted web page as a source (read in the background, with
	 * Browserbase when connected); answers with the notebook's full source list.
	 */
	readPage: (workspaceId: number, notebookId: number, url: string) =>
		baseApiService.post(
			`${researchPath(workspaceId, notebookId)}/read-page`,
			z.array(notebookSourceSchema),
			{ body: { url } }
		),
	/** Imports the chosen results; answers with the notebook's full source list. */
	importResults: (workspaceId: number, notebookId: number, items: ResearchImportItem[]) =>
		baseApiService.post(
			`${researchPath(workspaceId, notebookId)}/import`,
			z.array(notebookSourceSchema),
			{
				body: { items },
			}
		),
};
