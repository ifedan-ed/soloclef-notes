import { z } from "zod";
import { paginationQueryParams } from ".";

export const documentTypeEnum = z.enum([
	"EXTENSION",
	"CRAWLED_URL",
	"FILE",
	"YOUTUBE_VIDEO",
	"GOOGLE_DRIVE_FILE",
	"LOCAL_FOLDER_FILE",
	"ARTIFACT",
	"NOTE",
	"USER_MEMORY",
	"TEAM_MEMORY",
]);

export const document = z.object({
	id: z.number(),
	title: z.string(),
	document_type: documentTypeEnum,
	document_metadata: z.record(z.string(), z.any()),
	content: z.string(),
	content_preview: z.string().optional().default(""),
	content_hash: z.string(),
	unique_identifier_hash: z.string().nullable(),
	created_at: z.string(),
	updated_at: z.string().nullable(),
	workspace_id: z.number(),
	created_by_id: z.string().nullable().optional(),
	created_by_name: z.string().nullable().optional(),
	created_by_email: z.string().nullable().optional(),
});

export const extensionDocumentContent = z.object({
	metadata: z.object({
		BrowsingSessionId: z.string(),
		VisitedWebPageURL: z.string(),
		VisitedWebPageTitle: z.string(),
		VisitedWebPageDateWithTimeInISOString: z.string(),
		VisitedWebPageReffererURL: z.string(),
		VisitedWebPageVisitDurationInMilliseconds: z.string(),
	}),
	pageContent: z.string(),
});

export const documentWithChunks = document.extend({
	chunks: z.array(
		z.object({
			id: z.number(),
			content: z.string(),
			created_at: z.string(),
			/** 1-based first page of the original PDF this chunk was read from; null when unknown. */
			page_start: z.number().nullable().optional(),
			page_end: z.number().nullable().optional(),
		})
	),
	total_chunks: z.number().optional().default(0),
	chunk_start_index: z.number().optional().default(0),
});

/**
 * Get documents
 */
export const documentSortByEnum = z.enum(["created_at", "title", "document_type"]);
export const sortOrderEnum = z.enum(["asc", "desc"]);

export const getDocumentsRequest = z.object({
	queryParams: paginationQueryParams
		.extend({
			workspace_id: z.number().or(z.string()).optional(),
			document_types: z.array(documentTypeEnum).optional(),
			sort_by: documentSortByEnum.optional(),
			sort_order: sortOrderEnum.optional(),
		})
		.nullish(),
});

export const getDocumentsResponse = z.object({
	items: z.array(document),
	total: z.number(),
	page: z.number(),
	page_size: z.number(),
	has_more: z.boolean(),
});

/**
 * Get document
 */
export const getDocumentRequest = document.pick({ id: true });

export const getDocumentResponse = document;

/**
 * Create documents
 */
export const createDocumentRequest = document
	.pick({ document_type: true, workspace_id: true })
	.extend({
		content: z.string().or(z.array(z.string())).or(z.array(extensionDocumentContent)),
	});

export const createDocumentResponse = z.object({
	message: z.literal("Documents queued for background processing"),
	status: z.literal("queued"),
});

/**
 * Upload documents
 */
/**
 * Which parser reads an upload: "local" (pymupdf4llm on the server) or
 * "cloud" (the service ETL_CLOUD_SERVICE names). Replaces the old
 * Basic/Premium toggle, which meant something different per parser.
 */
export const documentParserEnum = z.enum(["local", "cloud"]);

/**
 * One parser choice as the deployment is configured right now. The backend
 * owns this table (app/etl_pipeline/capabilities.py), so the UI never keeps
 * its own list of which parser opens which file.
 */
export const parserCapability = z.object({
	parser: documentParserEnum,
	service: z.string().nullable(),
	available: z.boolean(),
	/** "missing_api_key" | "not_installed" | "unknown_service" | null. */
	unavailable_reason: z.string().nullable(),
	/** Page-credit multiplier. 0 = free. */
	page_multiplier: z.number().int().min(0),
	document_extensions: z.array(z.string()),
});

export const etlCapabilities = z.object({
	default_parser: documentParserEnum,
	parsers: z.array(parserCapability),
	/** File types read the same way whichever parser is chosen. */
	always_handled: z.object({
		plaintext: z.array(z.string()),
		direct_convert: z.array(z.string()),
		audio: z.array(z.string()),
		/** Read by the vision LLM when asked for; otherwise by the parser. */
		image: z.array(z.string()),
	}),
	page_credit_billing_enabled: z.boolean(),
});

export const uploadDocumentRequest = z.object({
	files: z.array(z.instanceof(File)),
	workspace_id: z.number(),
	use_vision_llm: z.boolean().default(false),
	/** Omitted: the server default parser applies. */
	parser: documentParserEnum.optional(),
});

export const uploadDocumentResponse = z.object({
	message: z.literal("Files uploaded for processing"),
	document_ids: z.array(z.number()),
	duplicate_document_ids: z.array(z.number()).optional(),
	total_files: z.number().optional(),
	pending_files: z.number().optional(),
	skipped_duplicates: z.number().optional(),
});

/**
 * Batch document status
 */
export const getDocumentsStatusRequest = z.object({
	queryParams: z.object({
		workspace_id: z.number(),
		document_ids: z.array(z.number()).min(1),
	}),
});

export const documentStatus = z.object({
	state: z.enum(["ready", "pending", "processing", "failed"]),
	reason: z.string().nullable().optional(),
});

export const documentStatusItem = z.object({
	id: z.number(),
	title: z.string(),
	document_type: documentTypeEnum,
	status: documentStatus,
});

export const getDocumentsStatusResponse = z.object({
	items: z.array(documentStatusItem),
});

/**
 * Search documents
 */
export const searchDocumentsRequest = z.object({
	queryParams: paginationQueryParams
		.extend({
			workspace_id: z.number().or(z.string()).optional(),
			document_types: z.array(documentTypeEnum).optional(),
			title: z.string().optional(),
		})
		.nullish(),
});

export const searchDocumentsResponse = z.object({
	items: z.array(document),
	total: z.number(),
	page: z.number(),
	page_size: z.number(),
	has_more: z.boolean(),
});

/**
 * Search document titles (lightweight, for mention picker)
 */
export const documentTitleRead = z.object({
	id: z.number(),
	title: z.string(),
	document_type: documentTypeEnum,
});

export const searchDocumentTitlesRequest = z.object({
	queryParams: z.object({
		workspace_id: z.number(),
		title: z.string().optional(),
		page: z.number().optional(),
		page_size: z.number().optional(),
	}),
});

export const searchDocumentTitlesResponse = z.object({
	items: z.array(documentTitleRead),
	has_more: z.boolean(),
});

/**
 * Get document type counts
 */
export const getDocumentTypeCountsRequest = z.object({
	queryParams: z
		.object({
			workspace_id: z.number().or(z.string()).optional(),
		})
		.nullish(),
});

export const getDocumentTypeCountsResponse = z.record(z.string(), z.number());

/**
 * Get document by chunk
 */
export const getDocumentByChunkRequest = z.object({
	chunk_id: z.number(),
	chunk_window: z.number().optional(),
});

export const getDocumentByChunkResponse = documentWithChunks;

/**
 * Get paginated chunks for a document
 */
export const getDocumentChunksRequest = z.object({
	document_id: z.number(),
	page: z.number().optional().default(0),
	page_size: z.number().optional().default(20),
	start_offset: z.number().optional(),
});

export const chunkRead = z.object({
	id: z.number(),
	content: z.string(),
	document_id: z.number(),
	created_at: z.string(),
	page_start: z.number().nullable().optional(),
	page_end: z.number().nullable().optional(),
});

export const getDocumentChunksResponse = z.object({
	items: z.array(chunkRead),
	total: z.number(),
	page: z.number(),
	page_size: z.number(),
	has_more: z.boolean(),
});

/**
 * Delete document
 */
export const deleteDocumentRequest = document.pick({ id: true });

export const deleteDocumentResponse = z.object({
	message: z.literal("Document deleted successfully"),
});

export type Document = z.infer<typeof document>;
export type DocumentTitleRead = z.infer<typeof documentTitleRead>;
export type GetDocumentsRequest = z.infer<typeof getDocumentsRequest>;
export type GetDocumentsResponse = z.infer<typeof getDocumentsResponse>;
export type GetDocumentRequest = z.infer<typeof getDocumentRequest>;
export type GetDocumentResponse = z.infer<typeof getDocumentResponse>;
export type CreateDocumentRequest = z.infer<typeof createDocumentRequest>;
export type CreateDocumentResponse = z.infer<typeof createDocumentResponse>;
export type UploadDocumentRequest = z.infer<typeof uploadDocumentRequest>;
export type UploadDocumentResponse = z.infer<typeof uploadDocumentResponse>;
export type GetDocumentsStatusRequest = z.infer<typeof getDocumentsStatusRequest>;
export type GetDocumentsStatusResponse = z.infer<typeof getDocumentsStatusResponse>;
export type DocumentStatus = z.infer<typeof documentStatus>;
export type DocumentStatusItem = z.infer<typeof documentStatusItem>;
export type SearchDocumentsRequest = z.infer<typeof searchDocumentsRequest>;
export type SearchDocumentsResponse = z.infer<typeof searchDocumentsResponse>;
export type SearchDocumentTitlesRequest = z.infer<typeof searchDocumentTitlesRequest>;
export type SearchDocumentTitlesResponse = z.infer<typeof searchDocumentTitlesResponse>;
export type GetDocumentTypeCountsRequest = z.infer<typeof getDocumentTypeCountsRequest>;
export type GetDocumentTypeCountsResponse = z.infer<typeof getDocumentTypeCountsResponse>;
export type GetDocumentByChunkRequest = z.infer<typeof getDocumentByChunkRequest>;
export type GetDocumentByChunkResponse = z.infer<typeof getDocumentByChunkResponse>;
export type DeleteDocumentRequest = z.infer<typeof deleteDocumentRequest>;
export type DeleteDocumentResponse = z.infer<typeof deleteDocumentResponse>;
export type DocumentTypeEnum = z.infer<typeof documentTypeEnum>;
export type DocumentSortBy = z.infer<typeof documentSortByEnum>;
export type SortOrder = z.infer<typeof sortOrderEnum>;
export type GetDocumentChunksRequest = z.infer<typeof getDocumentChunksRequest>;
export type GetDocumentChunksResponse = z.infer<typeof getDocumentChunksResponse>;
export type ChunkRead = z.infer<typeof chunkRead>;
export type DocumentParser = z.infer<typeof documentParserEnum>;
export type ParserCapability = z.infer<typeof parserCapability>;
export type EtlCapabilities = z.infer<typeof etlCapabilities>;
