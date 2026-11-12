import type { DocumentStage, GetDocumentsStatusResponse } from "@/contracts/types/document.types";
import type { NotebookSource } from "../model/notebook";

/**
 * The documents among a notebook's sources that are still being indexed,
 * in ascending id order. Sorted so the list is the same for the same
 * sources whatever order they are shown in: it is part of a query key, and
 * an unstable key would refetch on every render.
 */
export function indexingDocumentIds(sources: readonly NotebookSource[]): number[] {
	return sources
		.filter(
			(source) =>
				source.kind === "document" &&
				(source.status === "pending" || source.status === "processing")
		)
		.map((source) => source.target_id)
		.sort((a, b) => a - b);
}

/**
 * Each processing document's stage, by document id. A document that is not
 * processing has no entry: a stage left over on a finished document, or one
 * sent beside "pending", must not be shown as work in progress.
 */
export function stagesByDocument(
	items: GetDocumentsStatusResponse["items"]
): Map<number, DocumentStage> {
	const stages = new Map<number, DocumentStage>();
	for (const item of items) {
		if (item.status.state === "processing" && item.status.stage) {
			stages.set(item.id, item.status.stage);
		}
	}
	return stages;
}

/** The message key for a stage, in the `notebooks` namespace. */
export const SOURCE_STAGE_KEYS = {
	parsing: "source_stage_parsing",
	chunking: "source_stage_chunking",
	embedding: "source_stage_embedding",
	indexing: "source_stage_indexing",
	retrying: "source_stage_retrying",
} as const satisfies Record<DocumentStage, string>;
