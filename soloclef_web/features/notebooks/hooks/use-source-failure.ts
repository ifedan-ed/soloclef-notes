"use client";

import { useQuery } from "@tanstack/react-query";
import { documentsApiService } from "@/lib/apis/documents-api.service";

/**
 * Why a source could not be read, in the server's words (documents.status
 * reason), such as a scan with no text layer refused by the Local parser.
 *
 * The notebook's source list says only "failed"; without the reason the
 * source guide could say nothing more than that no guide exists, and the
 * person could not tell whether to re-index with another parser or upload a
 * different file. Read only for a failed document, once: a failure does not
 * change until the document is re-indexed, which the change stream reports
 * on the "documents" keys this read sits under.
 */
export function useSourceFailure(documentId: number, enabled: boolean) {
	return useQuery({
		queryKey: ["documents", "failure", documentId],
		queryFn: async () => {
			const { items } = await documentsApiService.getDocumentsStatus({
				queryParams: { document_ids: [documentId] },
			});
			return items.find((item) => item.id === documentId)?.status.reason ?? null;
		},
		enabled,
		meta: { suppressGlobalErrorToast: true },
	});
}
