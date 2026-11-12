"use client";

import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import type { DocumentStage } from "@/contracts/types/document.types";
import { documentsApiService } from "@/lib/apis/documents-api.service";
import { indexingDocumentIds, stagesByDocument } from "../lib/source-stages";
import type { NotebookSource } from "../model/notebook";

const NO_STAGES: ReadonlyMap<number, DocumentStage> = new Map();

/**
 * What the indexer is doing with each source that is still being indexed.
 *
 * The notebook's source list says only that a source is not ready. The
 * status poll says which step it is on, so a slow upload shows whether it
 * is waiting on the parser or on the embeddings. Only the documents being
 * indexed are asked about, and nothing is asked once none is.
 *
 * The stage is a courtesy beside the spinner. If the poll fails the row
 * keeps its spinner and shows no stage; it does not show an error for
 * something that is not the upload failing.
 */
export function useSourceStages(
	sources: readonly NotebookSource[]
): ReadonlyMap<number, DocumentStage> {
	const documentIds = useMemo(() => indexingDocumentIds(sources), [sources]);
	const { data } = useQuery({
		queryKey: ["documents", "stages", documentIds],
		queryFn: async () =>
			stagesByDocument(
				(
					await documentsApiService.getDocumentsStatus({
						queryParams: { document_ids: documentIds },
					})
				).items
			),
		enabled: documentIds.length > 0,
		// Faster than the source list's own five seconds: a stage can pass in
		// two, and a label that skips from "reading" to done explains nothing.
		refetchInterval: 2000,
		// A finished document's last stage is not kept around for the next one.
		gcTime: 0,
	});
	return documentIds.length > 0 && data ? data : NO_STAGES;
}
