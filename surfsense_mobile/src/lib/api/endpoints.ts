/**
 * The backend routes the app uses, one function each, with the same paths and
 * bodies as surfsense_web/features/notebooks/api/notebooks-api.ts.
 */
import { z } from "zod";
import type { ApiClient } from "./client";
import {
	artifactManifestSchema,
	billingMeSchema,
	documentByChunkSchema,
	etlCapabilitiesSchema,
	followupsSchema,
	languageOptionsSchema,
	notebookSchema,
	notebookSearchSchema,
	notebookSourceSchema,
	notebookThreadSchema,
	type Notebook,
	reportTemplateSchema,
	sourceGuideSchema,
	studioOutputSchema,
	studioSuggestionsSchema,
	threadHistorySchema,
	userSchema,
	workspaceSchema,
} from "./schemas";

const base = (workspaceId: number) => `/api/v1/workspaces/${workspaceId}/notebooks`;
const one = (workspaceId: number, notebookId: number) => `${base(workspaceId)}/${notebookId}`;

export type NotebookChanges = Partial<Pick<Notebook, "title" | "icon">>;

export interface SuggestionsQuery {
	variant?: string;
	language?: string;
	refresh?: boolean;
}

export type SourceTarget = { kind: "document" | "folder"; id: number };

export interface ResearchImportItem {
	url: string;
	title: string;
	snippet?: string;
	source: string;
}

export function suggestionsPath(
	workspaceId: number,
	notebookId: number,
	kind: string,
	{ variant, language, refresh }: SuggestionsQuery = {}
): string {
	const query = new URLSearchParams({ kind });
	if (variant) query.set("variant", variant);
	if (language) query.set("language", language);
	if (refresh) query.set("refresh", "true");
	return `${one(workspaceId, notebookId)}/studio/suggestions?${query.toString()}`;
}

export function createEndpoints(api: ApiClient) {
	return {
		me: () => api.get("/users/me", userSchema),
		workspaces: () => api.get("/api/v1/workspaces", z.array(workspaceSchema)),

		notebooks: (workspaceId: number) => api.get(base(workspaceId), z.array(notebookSchema)),
		notebook: (workspaceId: number, notebookId: number) => api.get(one(workspaceId, notebookId), notebookSchema),
		/** Passages inside the workspace's notebooks (sources and notes) that match ``query``. */
		searchNotebooks: (workspaceId: number, query: string) =>
			api.get(`${base(workspaceId)}/search?${new URLSearchParams({ q: query }).toString()}`, notebookSearchSchema),
		createNotebook: (workspaceId: number, body: { title: string; icon?: string | null }) =>
			api.post(base(workspaceId), body, notebookSchema),
		updateNotebook: (workspaceId: number, notebookId: number, body: NotebookChanges) =>
			api.patch(one(workspaceId, notebookId), body, notebookSchema),
		deleteNotebook: (workspaceId: number, notebookId: number) => api.delete(one(workspaceId, notebookId)),

		sources: (workspaceId: number, notebookId: number) =>
			api.get(`${one(workspaceId, notebookId)}/sources`, z.array(notebookSourceSchema)),
		addSources: (workspaceId: number, notebookId: number, targets: SourceTarget[]) =>
			api.post(`${one(workspaceId, notebookId)}/sources`, { targets }, z.array(notebookSourceSchema)),
		toggleSource: (workspaceId: number, notebookId: number, sourceId: number, enabled: boolean) =>
			api.patch(`${one(workspaceId, notebookId)}/sources/${sourceId}`, { enabled }, notebookSourceSchema),
		toggleAllSources: (workspaceId: number, notebookId: number, enabled: boolean) =>
			api.post(`${one(workspaceId, notebookId)}/sources/toggle`, { enabled }, z.array(notebookSourceSchema)),
		removeSource: (workspaceId: number, notebookId: number, sourceId: number) =>
			api.delete(`${one(workspaceId, notebookId)}/sources/${sourceId}`),
		sourceGuide: (workspaceId: number, notebookId: number, sourceId: number) =>
			api.get(`${one(workspaceId, notebookId)}/sources/${sourceId}/guide`, sourceGuideSchema),
		/** A pasted link, imported the way the web's research results are. */
		importLinks: (workspaceId: number, notebookId: number, items: ResearchImportItem[]) =>
			api.post(`${one(workspaceId, notebookId)}/research/import`, { items }, z.array(notebookSourceSchema)),
		etlCapabilities: () => api.get("/api/v1/etl/capabilities", etlCapabilitiesSchema),

		threads: (workspaceId: number, notebookId: number) =>
			api.get(`${one(workspaceId, notebookId)}/threads`, z.array(notebookThreadSchema)),
		createThread: (workspaceId: number, notebookId: number) =>
			api.post(`${one(workspaceId, notebookId)}/threads`, undefined, notebookThreadSchema),
		threadMessages: (threadId: number) => api.get(`/api/v1/threads/${threadId}`, threadHistorySchema),
		cancelTurn: (threadId: number) => api.post(`/api/v1/threads/${threadId}/cancel-active-turn`, undefined),
		followups: (workspaceId: number, notebookId: number, answer: string) =>
			api.post(`${one(workspaceId, notebookId)}/followups`, { answer }, followupsSchema),
		documentByChunk: (chunkId: number) =>
			api.get(`/api/v1/documents/by-chunk/${chunkId}?chunk_window=0`, documentByChunkSchema),

		outputs: (workspaceId: number, notebookId: number) =>
			api.get(`${one(workspaceId, notebookId)}/studio/outputs`, z.array(studioOutputSchema)),
		generate: (workspaceId: number, notebookId: number, body: Record<string, unknown>) =>
			api.post(`${one(workspaceId, notebookId)}/studio/generate`, body, studioOutputSchema),
		renameOutput: (workspaceId: number, notebookId: number, outputId: number, title: string) =>
			api.patch(`${one(workspaceId, notebookId)}/studio/outputs/${outputId}`, { title }, studioOutputSchema),
		deleteOutput: (workspaceId: number, notebookId: number, outputId: number) =>
			api.delete(`${one(workspaceId, notebookId)}/studio/outputs/${outputId}`),
		suggestions: (workspaceId: number, notebookId: number, kind: string, query: SuggestionsQuery = {}) =>
			api.get(suggestionsPath(workspaceId, notebookId, kind, query), studioSuggestionsSchema),
		reportTemplates: (workspaceId: number) =>
			api.get(`/api/v1/workspaces/${workspaceId}/notebook-report-templates`, z.array(reportTemplateSchema)),
		podcastLanguages: () => api.get("/api/v1/podcasts/languages", languageOptionsSchema),

		/** Plan and balances; plan names come back in ``locale`` (the backend reads X-Locale). */
		billingMe: (locale: string) =>
			api.request("/api/v1/billing/me", { schema: billingMeSchema, headers: { "X-Locale": locale } }),

		artifactManifest: (workspaceId: number, artifactId: number) =>
			api.get(`/api/v1/workspaces/${workspaceId}/artifacts/${artifactId}/manifest`, artifactManifestSchema),
	};
}

export type Endpoints = ReturnType<typeof createEndpoints>;

export function artifactDownloadPath(workspaceId: number, artifactId: number): string {
	return `/api/v1/workspaces/${workspaceId}/artifacts/${artifactId}/download`;
}
