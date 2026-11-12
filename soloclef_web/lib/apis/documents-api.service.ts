import {
	type CreateDocumentRequest,
	createDocumentRequest,
	createDocumentResponse,
	type DeleteDocumentRequest,
	type DocumentParser,
	deleteDocumentRequest,
	deleteDocumentResponse,
	etlCapabilities,
	type GetDocumentByChunkRequest,
	type GetDocumentChunksRequest,
	type GetDocumentRequest,
	type GetDocumentsRequest,
	type GetDocumentsStatusRequest,
	type GetDocumentTypeCountsRequest,
	getDocumentByChunkRequest,
	getDocumentByChunkResponse,
	getDocumentChunksRequest,
	getDocumentChunksResponse,
	getDocumentRequest,
	getDocumentResponse,
	getDocumentsRequest,
	getDocumentsResponse,
	getDocumentsStatusRequest,
	getDocumentsStatusResponse,
	getDocumentTypeCountsRequest,
	getDocumentTypeCountsResponse,
	type SearchDocumentsRequest,
	type SearchDocumentTitlesRequest,
	searchDocumentsRequest,
	searchDocumentsResponse,
	searchDocumentTitlesRequest,
	searchDocumentTitlesResponse,
	type UploadDocumentRequest,
	uploadDocumentRequest,
	uploadDocumentResponse,
} from "@/contracts/types/document.types";
import { folderListResponse } from "@/contracts/types/folder.types";
import { ValidationError } from "../error";
import { baseApiService } from "./base-api.service";
import { uploadFormData } from "./upload-form";

class DocumentsApiService {
	/**
	 * Get a list of documents with optional filtering and pagination
	 */
	getDocuments = async (request: GetDocumentsRequest) => {
		const parsedRequest = getDocumentsRequest.safeParse(request);

		if (!parsedRequest.success) {
			console.error("Invalid request:", parsedRequest.error);

			const errorMessage = parsedRequest.error.issues.map((issue) => issue.message).join(", ");
			throw new ValidationError(`Invalid request: ${errorMessage}`);
		}

		// Transform query params to be string values
		const transformedQueryParams = parsedRequest.data.queryParams
			? Object.fromEntries(
					Object.entries(parsedRequest.data.queryParams).map(([k, v]) => {
						const key = k;
						// Handle array values (document_type)
						if (Array.isArray(v)) {
							return [key, v.join(",")];
						}
						return [key, String(v)];
					})
				)
			: undefined;

		const queryParams = transformedQueryParams
			? new URLSearchParams(transformedQueryParams).toString()
			: "";

		return baseApiService.get(`/api/v1/documents?${queryParams}`, getDocumentsResponse);
	};

	/**
	 * Get a single document by ID
	 */
	getDocument = async (request: GetDocumentRequest) => {
		const parsedRequest = getDocumentRequest.safeParse(request);

		if (!parsedRequest.success) {
			console.error("Invalid request:", parsedRequest.error);

			const errorMessage = parsedRequest.error.issues.map((issue) => issue.message).join(", ");
			throw new ValidationError(`Invalid request: ${errorMessage}`);
		}

		return baseApiService.get(`/api/v1/documents/${request.id}`, getDocumentResponse);
	};

	/**
	 * Add a note from a title and a text. It is answered as the document it
	 * became, already queued for indexing.
	 */
	createDocument = async (request: CreateDocumentRequest) => {
		const parsedRequest = createDocumentRequest.safeParse(request);

		if (!parsedRequest.success) {
			console.error("Invalid request:", parsedRequest.error);

			const errorMessage = parsedRequest.error.issues.map((issue) => issue.message).join(", ");
			throw new ValidationError(`Invalid request: ${errorMessage}`);
		}

		return baseApiService.post(`/api/v1/documents`, createDocumentResponse, {
			body: parsedRequest.data,
		});
	};

	/**
	 * The parsers a source can choose among, with which are configured and
	 * what each reads.
	 *
	 * Deployment-wide and only changes on a restart, so callers cache it
	 * hard -- see etlCapabilities in document.types.ts.
	 */
	getEtlCapabilities = async () => baseApiService.get(`/api/v1/etl/capabilities`, etlCapabilities);

	/**
	 * Upload document files in batches to avoid proxy/LB timeouts.
	 * Files are split into chunks of UPLOAD_BATCH_SIZE and sent as separate
	 * requests. Results are aggregated into a single response.
	 */
	uploadDocument = async (request: UploadDocumentRequest) => {
		const parsedRequest = uploadDocumentRequest.safeParse(request);

		if (!parsedRequest.success) {
			console.error("Invalid request:", parsedRequest.error);

			const errorMessage = parsedRequest.error.issues.map((issue) => issue.message).join(", ");
			throw new ValidationError(`Invalid request: ${errorMessage}`);
		}

		const { files, use_vision_llm, parser } = parsedRequest.data;
		const UPLOAD_BATCH_SIZE = 5;

		const batches: File[][] = [];
		for (let i = 0; i < files.length; i += UPLOAD_BATCH_SIZE) {
			batches.push(files.slice(i, i + UPLOAD_BATCH_SIZE));
		}

		const allDocumentIds: number[] = [];
		const allDuplicateIds: number[] = [];
		let totalFiles = 0;
		let pendingFiles = 0;
		let skippedDuplicates = 0;

		for (const batch of batches) {
			const formData = uploadFormData({
				files: batch,
				parser,
				useVisionLlm: use_vision_llm,
			});

			const controller = new AbortController();
			const timeoutId = setTimeout(() => controller.abort(), 120_000);

			try {
				const result = await baseApiService.postFormData(
					`/api/v1/documents/fileupload`,
					uploadDocumentResponse,
					{ body: formData, signal: controller.signal }
				);

				allDocumentIds.push(...(result.document_ids ?? []));
				allDuplicateIds.push(...(result.duplicate_document_ids ?? []));
				totalFiles += result.total_files ?? batch.length;
				pendingFiles += result.pending_files ?? 0;
				skippedDuplicates += result.skipped_duplicates ?? 0;
			} finally {
				clearTimeout(timeoutId);
			}
		}

		return {
			message: "Files uploaded for processing" as const,
			document_ids: allDocumentIds,
			duplicate_document_ids: allDuplicateIds,
			total_files: totalFiles,
			pending_files: pendingFiles,
			skipped_duplicates: skippedDuplicates,
		};
	};

	/**
	 * Batch document status for async processing tracking
	 */
	getDocumentsStatus = async (request: GetDocumentsStatusRequest) => {
		const parsedRequest = getDocumentsStatusRequest.safeParse(request);

		if (!parsedRequest.success) {
			console.error("Invalid request:", parsedRequest.error);
			const errorMessage = parsedRequest.error.issues.map((issue) => issue.message).join(", ");
			throw new ValidationError(`Invalid request: ${errorMessage}`);
		}

		const params = new URLSearchParams({
			document_ids: parsedRequest.data.queryParams.document_ids.join(","),
		});

		return baseApiService.get(
			`/api/v1/documents/status?${params.toString()}`,
			getDocumentsStatusResponse
		);
	};

	/**
	 * Search documents by title
	 */
	searchDocuments = async (request: SearchDocumentsRequest) => {
		const parsedRequest = searchDocumentsRequest.safeParse(request);

		if (!parsedRequest.success) {
			console.error("Invalid request:", parsedRequest.error);

			const errorMessage = parsedRequest.error.issues.map((issue) => issue.message).join(", ");
			throw new ValidationError(`Invalid request: ${errorMessage}`);
		}

		// Transform query params to be string values
		const transformedQueryParams = parsedRequest.data.queryParams
			? Object.fromEntries(
					Object.entries(parsedRequest.data.queryParams).map(([k, v]) => {
						const key = k;
						// Handle array values (document_type)
						if (Array.isArray(v)) {
							return [key, v.join(",")];
						}
						return [key, String(v)];
					})
				)
			: undefined;

		const queryParams = transformedQueryParams
			? new URLSearchParams(transformedQueryParams).toString()
			: "";

		return baseApiService.get(`/api/v1/documents/search?${queryParams}`, searchDocumentsResponse);
	};

	/**
	 * Search document titles (lightweight, optimized for mention picker)
	 * Returns only id, title, document_type - no content or metadata
	 * @param request - The search request with query params
	 * @param signal - Optional AbortSignal for request cancellation
	 */
	searchDocumentTitles = async (request: SearchDocumentTitlesRequest, signal?: AbortSignal) => {
		const parsedRequest = searchDocumentTitlesRequest.safeParse(request);

		if (!parsedRequest.success) {
			console.error("Invalid request:", parsedRequest.error);

			const errorMessage = parsedRequest.error.issues.map((issue) => issue.message).join(", ");
			throw new ValidationError(`Invalid request: ${errorMessage}`);
		}

		// Transform query params to be string values
		const transformedQueryParams = Object.fromEntries(
			Object.entries(parsedRequest.data.queryParams)
				.filter(([, v]) => v !== undefined)
				.map(([k, v]) => [k, String(v)])
		);

		const queryParams = new URLSearchParams(transformedQueryParams).toString();

		return baseApiService.get(
			`/api/v1/documents/search/titles?${queryParams}`,
			searchDocumentTitlesResponse,
			{ signal }
		);
	};

	/**
	 * Get document type counts
	 */
	getDocumentTypeCounts = async (request: GetDocumentTypeCountsRequest) => {
		// Validate the request
		const parsedRequest = getDocumentTypeCountsRequest.safeParse(request);

		if (!parsedRequest.success) {
			console.error("Invalid request:", parsedRequest.error);

			// Format a user friendly error message
			const errorMessage = parsedRequest.error.issues.map((issue) => issue.message).join(", ");
			throw new ValidationError(`Invalid request: ${errorMessage}`);
		}

		// No query string: the counts are the signed-in account's.
		return baseApiService.get(`/api/v1/documents/type-counts`, getDocumentTypeCountsResponse);
	};

	/**
	 * Get document by chunk ID (includes a window of chunks around the cited one)
	 */
	getDocumentByChunk = async (request: GetDocumentByChunkRequest) => {
		const parsedRequest = getDocumentByChunkRequest.safeParse(request);

		if (!parsedRequest.success) {
			console.error("Invalid request:", parsedRequest.error);

			const errorMessage = parsedRequest.error.issues.map((issue) => issue.message).join(", ");
			throw new ValidationError(`Invalid request: ${errorMessage}`);
		}

		const params = new URLSearchParams();
		if (request.chunk_window != null) {
			params.set("chunk_window", String(request.chunk_window));
		}
		const qs = params.toString();
		const url = `/api/v1/chunks/${request.chunk_id}/document${qs ? `?${qs}` : ""}`;

		return baseApiService.get(url, getDocumentByChunkResponse);
	};

	/**
	 * Get paginated chunks for a document
	 */
	getDocumentChunks = async (request: GetDocumentChunksRequest) => {
		const parsedRequest = getDocumentChunksRequest.safeParse(request);

		if (!parsedRequest.success) {
			console.error("Invalid request:", parsedRequest.error);

			const errorMessage = parsedRequest.error.issues.map((issue) => issue.message).join(", ");
			throw new ValidationError(`Invalid request: ${errorMessage}`);
		}

		const params = new URLSearchParams({
			page: String(parsedRequest.data.page),
			page_size: String(parsedRequest.data.page_size),
		});
		if (parsedRequest.data.start_offset != null) {
			params.set("start_offset", String(parsedRequest.data.start_offset));
		}

		return baseApiService.get(
			`/api/v1/documents/${parsedRequest.data.document_id}/chunks?${params}`,
			getDocumentChunksResponse
		);
	};

	listDocumentVersions = async (documentId: number) => {
		return baseApiService.get(`/api/v1/documents/${documentId}/versions`);
	};

	getDocumentVersion = async (documentId: number, versionNumber: number) => {
		return baseApiService.get(`/api/v1/documents/${documentId}/versions/${versionNumber}`);
	};

	restoreDocumentVersion = async (documentId: number, versionNumber: number) => {
		return baseApiService.post(`/api/v1/documents/${documentId}/versions/${versionNumber}/restore`);
	};

	folderMtimeCheck = async (body: {
		folder_name: string;
		files: { relative_path: string; mtime: number }[];
	}): Promise<{ files_to_upload: string[] }> => {
		return baseApiService.post(`/api/v1/documents/folder-mtime-check`, undefined, {
			body,
		}) as unknown as { files_to_upload: string[] };
	};

	folderUploadFiles = async (
		files: File[],
		metadata: {
			folder_name: string;
			relative_paths: string[];
			root_folder_id?: number | null;
			use_vision_llm?: boolean;
			/** Omitted: the server default parser applies. */
			parser?: DocumentParser;
		},
		signal?: AbortSignal
	): Promise<{ message: string; status: string; root_folder_id: number; file_count: number }> => {
		// Text fields before the files, as uploadFormData does and for its
		// reason: a server that streams the files needs the fields first.
		const formData = new FormData();
		formData.append("folder_name", metadata.folder_name);
		formData.append("relative_paths", JSON.stringify(metadata.relative_paths));
		if (metadata.root_folder_id != null) {
			formData.append("root_folder_id", String(metadata.root_folder_id));
		}
		formData.append("use_vision_llm", String(metadata.use_vision_llm ?? false));
		if (metadata.parser) formData.append("parser", metadata.parser);
		for (const file of files) {
			formData.append("files", file);
		}

		const totalSize = files.reduce((acc, f) => acc + f.size, 0);
		const timeoutMs = Math.min(Math.max((totalSize / (1024 * 1024)) * 5000, 30_000), 600_000);

		const controller = new AbortController();
		const timeoutId = setTimeout(() => controller.abort(), timeoutMs);

		if (signal) {
			signal.addEventListener("abort", () => controller.abort(), { once: true });
		}

		try {
			return (await baseApiService.postFormData(`/api/v1/documents/folder-upload`, undefined, {
				body: formData,
				signal: controller.signal,
			})) as { message: string; status: string; root_folder_id: number; file_count: number };
		} finally {
			clearTimeout(timeoutId);
		}
	};

	folderNotifyUnlinked = async (body: {
		folder_name: string;
		root_folder_id: number | null;
		relative_paths: string[];
	}): Promise<{ deleted_count: number }> => {
		return baseApiService.post(`/api/v1/documents/folder-unlink`, undefined, {
			body,
		}) as unknown as { deleted_count: number };
	};

	folderSyncFinalize = async (body: {
		folder_name: string;
		root_folder_id: number | null;
		all_relative_paths: string[];
	}): Promise<{ deleted_count: number }> => {
		return baseApiService.post(`/api/v1/documents/folder-sync-finalize`, undefined, {
			body,
		}) as unknown as { deleted_count: number };
	};

	/** The account's top folders that the desktop app keeps in step with a local folder. */
	getWatchedFolders = async () => {
		return baseApiService.get(`/api/v1/documents/watched-folders`, folderListResponse);
	};

	/**
	 * Delete a document
	 */
	deleteDocument = async (request: DeleteDocumentRequest) => {
		// Validate the request
		const parsedRequest = deleteDocumentRequest.safeParse(request);

		if (!parsedRequest.success) {
			console.error("Invalid request:", parsedRequest.error);

			// Format a user friendly error message
			const errorMessage = parsedRequest.error.issues.map((issue) => issue.message).join(", ");
			throw new ValidationError(`Invalid request: ${errorMessage}`);
		}

		return baseApiService.delete(`/api/v1/documents/${request.id}`, deleteDocumentResponse);
	};
}

export const documentsApiService = new DocumentsApiService();
