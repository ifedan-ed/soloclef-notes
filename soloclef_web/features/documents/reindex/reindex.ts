import { z } from "zod";
import type { DocumentParser } from "@/contracts/types/document.types";
import { baseApiService } from "@/lib/apis/base-api.service";

const reindexResponse = z.object({
	message: z.string(),
	document_id: z.number(),
});

/** The route, with `parser` named only when the person picked a different one. */
export function reindexPath(documentId: number, parser?: DocumentParser): string {
	const path = `/api/v1/documents/${documentId}/reindex`;
	return parser ? `${path}?parser=${encodeURIComponent(parser)}` : path;
}

/**
 * Hand a document back to the indexer. Without `parser` the document keeps
 * the parser it has; with one, that parser is recorded on the document and
 * reads its stored file from now on.
 */
export function reindexDocument(documentId: number, parser?: DocumentParser) {
	return baseApiService.post(reindexPath(documentId, parser), reindexResponse);
}

/**
 * Why a re-index was not queued.
 * - "busy": the document is being indexed or deleted already (409), so
 *   there is nothing to do but wait.
 * - "unavailable": the job queue did not answer (503); trying again later
 *   is the fix.
 * - "refused": the request itself cannot be done (400: the parser does not
 *   read this file, or the document has no file for a parser to read).
 * - "failed": anything else.
 */
export type ReindexFailureKind = "busy" | "unavailable" | "refused" | "failed";

export interface ReindexFailure {
	kind: ReindexFailureKind;
	/**
	 * The server's own sentence, when it sent one. It is shown in preference
	 * to anything worded here, because the server knows which of several
	 * reasons applies (one status covers "already being indexed", "being
	 * deleted" and "nothing to index").
	 */
	sentence: string | null;
}

const KIND_BY_STATUS: Record<number, ReindexFailureKind> = {
	400: "refused",
	409: "busy",
	503: "unavailable",
};

export function reindexFailure(error: unknown): ReindexFailure {
	if (typeof error !== "object" || error === null) return { kind: "failed", sentence: null };
	const { status, message } = error as { status?: unknown; message?: unknown };
	const kind = typeof status === "number" ? (KIND_BY_STATUS[status] ?? "failed") : "failed";
	// A request that never reached the server has no status; its message is
	// about the network, which is the sentence to show.
	return { kind, sentence: typeof message === "string" && message ? message : null };
}
