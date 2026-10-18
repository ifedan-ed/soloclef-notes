import { z } from "zod";
import { baseApiService } from "@/lib/apis/base-api.service";
import type { StudioRequestBody } from "../lib/studio-request";
import {
	type ChatGoal,
	notebookNoteSchema,
	notebookSchema,
	notebookSearchSchema,
	notebookSourceSchema,
	notebookThreadSchema,
	outputViewSchema,
	type ResponseLength,
	reportTemplateSchema,
	reportViewSchema,
	sourceGuideSchema,
	studioOutputSchema,
	studioSuggestionsSchema,
} from "../model/notebook";

const base = (workspaceId: number) => `/api/v1/workspaces/${workspaceId}/notebooks`;
const one = (workspaceId: number, notebookId: number) => `${base(workspaceId)}/${notebookId}`;

export type SourceTarget = { kind: "document" | "folder"; id: number };

export type SuggestionsQuery = { variant?: string; language?: string; refresh?: boolean };

export type NotebookChanges = {
	title?: string;
	icon?: string | null;
	chat_goal?: ChatGoal;
	chat_instructions?: string | null;
	response_length?: ResponseLength;
};

export const notebooksApi = {
	list: (workspaceId: number) => baseApiService.get(base(workspaceId), z.array(notebookSchema)),
	/** Passages inside the workspace's notebooks (sources and notes) that match ``query``. */
	search: (workspaceId: number, query: string, limit?: number) => {
		const params = new URLSearchParams({ q: query });
		if (limit) params.set("limit", String(limit));
		return baseApiService.get(
			`${base(workspaceId)}/search?${params.toString()}`,
			notebookSearchSchema
		);
	},
	get: (workspaceId: number, notebookId: number) =>
		baseApiService.get(one(workspaceId, notebookId), notebookSchema),
	create: (workspaceId: number, body: { title: string; icon?: string | null }) =>
		baseApiService.post(base(workspaceId), notebookSchema, { body }),
	update: (workspaceId: number, notebookId: number, body: NotebookChanges) =>
		baseApiService.patch(one(workspaceId, notebookId), notebookSchema, { body }),
	remove: (workspaceId: number, notebookId: number) =>
		baseApiService.delete(one(workspaceId, notebookId)),
	copy: (workspaceId: number, notebookId: number, title: string) =>
		baseApiService.post(`${one(workspaceId, notebookId)}/copy`, notebookSchema, {
			body: { title },
		}),
	setFeatured: (workspaceId: number, notebookId: number, featured: boolean) =>
		featured
			? baseApiService.put(`${one(workspaceId, notebookId)}/feature`, notebookSchema)
			: baseApiService.delete(`${one(workspaceId, notebookId)}/feature`, notebookSchema),
	deleteChatHistory: (workspaceId: number, notebookId: number) =>
		baseApiService.delete(`${one(workspaceId, notebookId)}/threads`),
	followups: (workspaceId: number, notebookId: number, answer: string) =>
		baseApiService.post(
			`${one(workspaceId, notebookId)}/followups`,
			z.object({ suggestions: z.array(z.string()) }),
			{ body: { answer } }
		),

	sources: (workspaceId: number, notebookId: number) =>
		baseApiService.get(`${one(workspaceId, notebookId)}/sources`, z.array(notebookSourceSchema)),
	addSources: (workspaceId: number, notebookId: number, targets: SourceTarget[]) =>
		baseApiService.post(`${one(workspaceId, notebookId)}/sources`, z.array(notebookSourceSchema), {
			body: { targets },
		}),
	toggleSource: (workspaceId: number, notebookId: number, sourceId: number, enabled: boolean) =>
		baseApiService.patch(
			`${one(workspaceId, notebookId)}/sources/${sourceId}`,
			notebookSourceSchema,
			{ body: { enabled } }
		),
	toggleAllSources: (workspaceId: number, notebookId: number, enabled: boolean) =>
		baseApiService.post(
			`${one(workspaceId, notebookId)}/sources/toggle`,
			z.array(notebookSourceSchema),
			{ body: { enabled } }
		),
	removeSource: (workspaceId: number, notebookId: number, sourceId: number) =>
		baseApiService.delete(`${one(workspaceId, notebookId)}/sources/${sourceId}`),
	sourceGuide: (workspaceId: number, notebookId: number, sourceId: number) =>
		baseApiService.get(
			`${one(workspaceId, notebookId)}/sources/${sourceId}/guide`,
			sourceGuideSchema
		),

	notes: (workspaceId: number, notebookId: number) =>
		baseApiService.get(`${one(workspaceId, notebookId)}/notes`, z.array(notebookNoteSchema)),
	createNote: (
		workspaceId: number,
		notebookId: number,
		body: { title: string; content: string; origin?: "hand" | "chat" }
	) => baseApiService.post(`${one(workspaceId, notebookId)}/notes`, notebookNoteSchema, { body }),
	note: (workspaceId: number, notebookId: number, noteId: number) =>
		baseApiService.get(`${one(workspaceId, notebookId)}/notes/${noteId}`, notebookNoteSchema),
	/** A copy of the note in another notebook of the workspace. */
	copyNote: (workspaceId: number, notebookId: number, noteId: number, targetNotebookId: number) =>
		baseApiService.post(
			`${one(workspaceId, notebookId)}/notes/${noteId}/copy`,
			notebookNoteSchema,
			{
				body: { notebook_id: targetNotebookId },
			}
		),
	updateNote: (
		workspaceId: number,
		notebookId: number,
		noteId: number,
		body: { title?: string; content?: string }
	) =>
		baseApiService.patch(`${one(workspaceId, notebookId)}/notes/${noteId}`, notebookNoteSchema, {
			body,
		}),
	deleteNote: (workspaceId: number, notebookId: number, noteId: number) =>
		baseApiService.delete(`${one(workspaceId, notebookId)}/notes/${noteId}`),
	convertNote: (workspaceId: number, notebookId: number, noteId: number) =>
		baseApiService.post(
			`${one(workspaceId, notebookId)}/notes/${noteId}/convert`,
			z.array(notebookSourceSchema)
		),

	threads: (workspaceId: number, notebookId: number) =>
		baseApiService.get(`${one(workspaceId, notebookId)}/threads`, z.array(notebookThreadSchema)),
	createThread: (workspaceId: number, notebookId: number) =>
		baseApiService.post(`${one(workspaceId, notebookId)}/threads`, notebookThreadSchema),

	outputs: (workspaceId: number, notebookId: number) =>
		baseApiService.get(
			`${one(workspaceId, notebookId)}/studio/outputs`,
			z.array(studioOutputSchema)
		),
	generate: (workspaceId: number, notebookId: number, body: StudioRequestBody) =>
		baseApiService.post(`${one(workspaceId, notebookId)}/studio/generate`, studioOutputSchema, {
			body,
		}),
	renameOutput: (workspaceId: number, notebookId: number, outputId: number, title: string) =>
		baseApiService.patch(
			`${one(workspaceId, notebookId)}/studio/outputs/${outputId}`,
			studioOutputSchema,
			{ body: { title } }
		),
	deleteOutput: (workspaceId: number, notebookId: number, outputId: number) =>
		baseApiService.delete(`${one(workspaceId, notebookId)}/studio/outputs/${outputId}`),
	suggestions: (
		workspaceId: number,
		notebookId: number,
		kind: string,
		{ variant, language, refresh }: SuggestionsQuery = {}
	) => {
		const query = new URLSearchParams({ kind });
		if (variant) query.set("variant", variant);
		if (language) query.set("language", language);
		if (refresh) query.set("refresh", "true");
		return baseApiService.get(
			`${one(workspaceId, notebookId)}/studio/suggestions?${query}`,
			studioSuggestionsSchema
		);
	},

	reportView: (workspaceId: number, notebookId: number, outputId: number) =>
		baseApiService.get(
			`${one(workspaceId, notebookId)}/studio/outputs/${outputId}/report-view`,
			reportViewSchema
		),
	addReportSuggestion: (
		workspaceId: number,
		notebookId: number,
		outputId: number,
		suggestionId: string
	) =>
		baseApiService.post(
			`${one(workspaceId, notebookId)}/studio/outputs/${outputId}/report-suggestions/${encodeURIComponent(suggestionId)}/add`,
			reportViewSchema
		),
	dismissReportSuggestion: (
		workspaceId: number,
		notebookId: number,
		outputId: number,
		suggestionId: string
	) =>
		baseApiService.post(
			`${one(workspaceId, notebookId)}/studio/outputs/${outputId}/report-suggestions/${encodeURIComponent(suggestionId)}/dismiss`,
			reportViewSchema
		),
	outputView: (workspaceId: number, notebookId: number, outputId: number) =>
		baseApiService.get(
			`${one(workspaceId, notebookId)}/studio/outputs/${outputId}/view`,
			outputViewSchema
		),
	reportTemplates: (workspaceId: number) =>
		baseApiService.get(
			`/api/v1/workspaces/${workspaceId}/notebook-report-templates`,
			z.array(reportTemplateSchema)
		),
	saveReportTemplate: (
		workspaceId: number,
		key: string,
		body: { title?: string; description?: string; prompt: string }
	) =>
		baseApiService.put(
			`/api/v1/workspaces/${workspaceId}/notebook-report-templates/${key}`,
			z.array(reportTemplateSchema),
			{ body }
		),
};
