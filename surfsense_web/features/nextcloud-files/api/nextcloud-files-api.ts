import { z } from "zod";
import type { DocumentParser } from "@/contracts/types/document.types";
import { baseApiService } from "@/lib/apis/base-api.service";

export const remoteEntrySchema = z.object({
	path: z.string(),
	name: z.string(),
	is_folder: z.boolean(),
	size: z.number().nullable(),
	modified: z.string().nullable(),
	content_type: z.string().nullable(),
	etag: z.string().nullable(),
});
export type RemoteEntry = z.infer<typeof remoteEntrySchema>;

const folderListingSchema = z.object({ path: z.string(), entries: z.array(remoteEntrySchema) });
const searchResultsSchema = z.object({ query: z.string(), entries: z.array(remoteEntrySchema) });

const importPreviewSchema = z.object({
	file_count: z.number(),
	total_bytes: z.number(),
	unsupported_count: z.number(),
	too_large_count: z.number(),
	over_limit: z.boolean(),
	max_files: z.number(),
});
export type ImportPreview = z.infer<typeof importPreviewSchema>;

const importResultSchema = z.object({
	document_ids: z.array(z.number()),
	folder_ids: z.array(z.number()),
	queued: z.number(),
	reused: z.number(),
	unsupported: z.array(z.string()),
	too_large: z.array(z.string()),
});
export type ImportResult = z.infer<typeof importResultSchema>;

export type PickedItem = { path: string; kind: "file" | "folder" };

export type ImportRequest = {
	items: PickedItem[];
	parser?: DocumentParser;
	use_vision_llm?: boolean;
	notebook_id?: number;
};

const refreshableDocumentsSchema = z.object({
	documents: z.array(z.object({ document_id: z.number(), connector_id: z.number() })),
});

const base = (connectorId: number) => `/api/v1/connectors/${connectorId}/nextcloud`;

/** The Nextcloud picker's backend: read-only browsing, then a one-time import. */
export const nextcloudFilesApi = {
	browse: (connectorId: number, path: string, signal?: AbortSignal) =>
		baseApiService.get(
			`${base(connectorId)}/browse?${new URLSearchParams({ path })}`,
			folderListingSchema,
			{ signal }
		),
	search: (connectorId: number, query: string, signal?: AbortSignal) =>
		baseApiService.get(
			`${base(connectorId)}/search?${new URLSearchParams({ q: query })}`,
			searchResultsSchema,
			{ signal }
		),
	preview: (connectorId: number, body: ImportRequest) =>
		baseApiService.post(`${base(connectorId)}/import/preview`, importPreviewSchema, { body }),
	import: (connectorId: number, body: ImportRequest) =>
		baseApiService.post(`${base(connectorId)}/import`, importResultSchema, { body }),
	refresh: (connectorId: number, documentId: number) =>
		baseApiService.post(
			`${base(connectorId)}/documents/${documentId}/refresh`,
			z.object({ status: z.enum(["unchanged", "queued"]) })
		),
	/** Documents this user can refresh, each with the connection that can. */
	refreshableDocuments: (workspaceId: number) =>
		baseApiService.get(
			`/api/v1/workspaces/${workspaceId}/nextcloud/refreshable-documents`,
			refreshableDocumentsSchema
		),
};
