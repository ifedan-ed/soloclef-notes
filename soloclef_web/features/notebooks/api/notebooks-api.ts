import { z } from "zod";
import { baseApiService } from "@/lib/apis/base-api.service";
import type { StudioRequestBody } from "../lib/studio-request";
import {
	type ChatGoal,
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

const base = "/api/v1/notebooks";
const one = (notebookId: number) => `${base}/${notebookId}`;

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
	list: () => baseApiService.get(base, z.array(notebookSchema)),
	/** Passages inside the account's notebooks (sources and notes) that match ``query``. */
	search: (query: string, limit?: number) => {
		const params = new URLSearchParams({ q: query });
		if (limit) params.set("limit", String(limit));
		return baseApiService.get(`${base}/search?${params.toString()}`, notebookSearchSchema);
	},
	get: (notebookId: number) => baseApiService.get(one(notebookId), notebookSchema),
	create: (body: { title: string; icon?: string | null }) =>
		baseApiService.post(base, notebookSchema, { body }),
	update: (notebookId: number, body: NotebookChanges) =>
		baseApiService.patch(one(notebookId), notebookSchema, { body }),
	remove: (notebookId: number) => baseApiService.delete(one(notebookId)),
	copy: (notebookId: number, title: string) =>
		baseApiService.post(`${one(notebookId)}/copy`, notebookSchema, {
			body: { title },
		}),
	setFeatured: (notebookId: number, featured: boolean) =>
		featured
			? baseApiService.put(`${one(notebookId)}/feature`, notebookSchema)
			: baseApiService.delete(`${one(notebookId)}/feature`, notebookSchema),
	deleteChatHistory: (notebookId: number) => baseApiService.delete(`${one(notebookId)}/threads`),
	followups: (notebookId: number, answer: string) =>
		baseApiService.post(
			`${one(notebookId)}/followups`,
			z.object({ suggestions: z.array(z.string()) }),
			{ body: { answer } }
		),

	sources: (notebookId: number) =>
		baseApiService.get(`${one(notebookId)}/sources`, z.array(notebookSourceSchema)),
	addSources: (notebookId: number, targets: SourceTarget[]) =>
		baseApiService.post(`${one(notebookId)}/sources`, z.array(notebookSourceSchema), {
			body: { targets },
		}),
	toggleSource: (notebookId: number, sourceId: number, enabled: boolean) =>
		baseApiService.patch(`${one(notebookId)}/sources/${sourceId}`, notebookSourceSchema, {
			body: { enabled },
		}),
	toggleAllSources: (notebookId: number, enabled: boolean) =>
		baseApiService.post(`${one(notebookId)}/sources/toggle`, z.array(notebookSourceSchema), {
			body: { enabled },
		}),
	removeSource: (notebookId: number, sourceId: number) =>
		baseApiService.delete(`${one(notebookId)}/sources/${sourceId}`),
	sourceGuide: (notebookId: number, sourceId: number) =>
		baseApiService.get(`${one(notebookId)}/sources/${sourceId}/guide`, sourceGuideSchema),

	threads: (notebookId: number) =>
		baseApiService.get(`${one(notebookId)}/threads`, z.array(notebookThreadSchema)),
	createThread: (notebookId: number) =>
		baseApiService.post(`${one(notebookId)}/threads`, notebookThreadSchema),

	outputs: (notebookId: number) =>
		baseApiService.get(`${one(notebookId)}/studio/outputs`, z.array(studioOutputSchema)),
	generate: (notebookId: number, body: StudioRequestBody) =>
		baseApiService.post(`${one(notebookId)}/studio/generate`, studioOutputSchema, {
			body,
		}),
	renameOutput: (notebookId: number, outputId: number, title: string) =>
		baseApiService.patch(`${one(notebookId)}/studio/outputs/${outputId}`, studioOutputSchema, {
			body: { title },
		}),
	deleteOutput: (notebookId: number, outputId: number) =>
		baseApiService.delete(`${one(notebookId)}/studio/outputs/${outputId}`),
	suggestions: (
		notebookId: number,
		kind: string,
		{ variant, language, refresh }: SuggestionsQuery = {}
	) => {
		const query = new URLSearchParams({ kind });
		if (variant) query.set("variant", variant);
		if (language) query.set("language", language);
		if (refresh) query.set("refresh", "true");
		return baseApiService.get(
			`${one(notebookId)}/studio/suggestions?${query}`,
			studioSuggestionsSchema
		);
	},

	reportView: (notebookId: number, outputId: number) =>
		baseApiService.get(
			`${one(notebookId)}/studio/outputs/${outputId}/report-view`,
			reportViewSchema
		),
	addReportSuggestion: (notebookId: number, outputId: number, suggestionId: string) =>
		baseApiService.post(
			`${one(notebookId)}/studio/outputs/${outputId}/report-suggestions/${encodeURIComponent(suggestionId)}/add`,
			reportViewSchema
		),
	dismissReportSuggestion: (notebookId: number, outputId: number, suggestionId: string) =>
		baseApiService.post(
			`${one(notebookId)}/studio/outputs/${outputId}/report-suggestions/${encodeURIComponent(suggestionId)}/dismiss`,
			reportViewSchema
		),
	outputView: (notebookId: number, outputId: number) =>
		baseApiService.get(`${one(notebookId)}/studio/outputs/${outputId}/view`, outputViewSchema),
	reportTemplates: () =>
		baseApiService.get(`/api/v1/notebook-report-templates`, z.array(reportTemplateSchema)),
	saveReportTemplate: (
		key: string,
		body: { title?: string; description?: string; prompt: string }
	) =>
		baseApiService.put(`/api/v1/notebook-report-templates/${key}`, z.array(reportTemplateSchema), {
			body,
		}),
};
