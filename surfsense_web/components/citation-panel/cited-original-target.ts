// What "Open" in the citation panel opens: the cited document's original, at
// the cited chunk's page, with the chunk's text to mark there.

import type { OpenDocumentViewerPayload } from "@/atoms/documents/document-viewer.atom";

interface CitedChunk {
	id: number;
	content: string;
	page_start?: number | null;
}

interface CitedDocument {
	id: number;
	workspace_id: number;
	title: string;
	chunks: readonly CitedChunk[];
}

export function citedOriginalTarget(
	document: CitedDocument,
	chunkId: number
): OpenDocumentViewerPayload {
	const cited = document.chunks.find((chunk) => chunk.id === chunkId);
	const page = cited?.page_start ?? null;
	return {
		documentId: document.id,
		workspaceId: document.workspace_id,
		title: document.title,
		initialPage: page,
		highlightText: page ? cited?.content : null,
	};
}

/** The button label: the page when it is known. */
export function openOriginalLabel(page: number | null | undefined): string {
	return page ? `Open p. ${page}` : "Open";
}
