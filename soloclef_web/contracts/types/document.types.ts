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
	// No owner fields: a document is the signed-in account's or it is not
	// answered at all, so the API names no owner and no creator id.
	created_by_name: z.string().nullable().optional(),
	created_by_email: z.string().nullable().optional(),
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
 * Add a note: a title and a text, optionally where it came from and the
 * folder it is filed in.
 */
export const createDocumentRequest = z.object({
	title: z.string().min(1).max(500),
	content: z.string().min(1),
	source_url: z.string().url().nullable().optional(),
	folder_id: z.number().int().positive().nullable().optional(),
});

export const createDocumentResponse = document;

/**
 * Upload documents
 */
/**
 * Which parser reads a source's files. Every source names one: an upload, a
 * notebook's "add sources", a Nextcloud import, a connection such as Drive.
 * "local" is the pymupdf4llm service on the server; the other three are
 * hosted APIs. The names are the server's own (soloclef_ingest/selection),
 * exact: there is no alias and no "default" spelling.
 */
export const documentParserEnum = z.enum(["local", "datalab", "llamaparse", "unstructured"]);

/**
 * One parser as this deployment has it. The server always lists all four,
 * so a parser with no key is shown as unavailable instead of being absent,
 * and the picker never keeps its own list of which parser opens which file.
 */
export const parserCapability = z.object({
	parser: documentParserEnum,
	available: z.boolean(),
	/** "not_configured" when unavailable, else null. A token to translate, not a sentence. */
	unavailable_reason: z.string().nullable(),
	/** What the parser reads, known whether or not it is configured. */
	document_extensions: z.array(z.string()),
});

export const etlCapabilities = z.object({
	default_parser: documentParserEnum,
	parsers: z.array(parserCapability),
	/**
	 * File types the server reads itself whichever parser is chosen: text is
	 * text, and sending it to a metered API would be paying for nothing.
	 */
	always_handled: z.object({
		plaintext: z.array(z.string()),
		direct_convert: z.array(z.string()),
	}),
	/**
	 * Whether the caller may switch the vision model on: they, or the server,
	 * have one. False hides the switch, as the upload would refuse it.
	 */
	vision_available: z.boolean(),
	/**
	 * Pictures an upload with vision on accepts whichever parser is chosen:
	 * the vision model describes them.
	 */
	picture_extensions: z.array(z.string()),
	max_file_size_bytes: z.number().int().positive(),
	max_files_per_upload: z.number().int().positive(),
});

export const uploadDocumentRequest = z.object({
	files: z.array(z.instanceof(File)),
	use_vision_llm: z.boolean().default(false),
	/**
	 * Required: the dialog sends nothing until the parser choices have
	 * loaded and one that can run is picked, so no upload leaves the choice
	 * to the server.
	 */
	parser: documentParserEnum,
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
		document_ids: z.array(z.number()).min(1),
	}),
});

/**
 * What the indexer is doing with a document that is "processing", so a slow
 * upload says what it is waiting on. "retrying" is a document waiting to be
 * tried again after a service it needs did not answer.
 */
export const documentStageEnum = z.enum([
	"parsing",
	"chunking",
	"embedding",
	"indexing",
	"retrying",
]);

export const documentStatus = z.object({
	state: z.enum(["ready", "pending", "processing", "failed"]),
	reason: z.string().nullable().optional(),
	/** Set only while the state is "processing". */
	stage: documentStageEnum.nullable().optional(),
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
// The counts are the account's own; there is nothing to ask for but them.
// The empty object is kept so this read has the same request shape as the
// others and its callers need not change again when a filter is added.
export const getDocumentTypeCountsRequest = z.object({
	queryParams: z.object({}).nullish(),
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
export type DocumentStage = z.infer<typeof documentStageEnum>;
export type ParserCapability = z.infer<typeof parserCapability>;
export type EtlCapabilities = z.infer<typeof etlCapabilities>;
