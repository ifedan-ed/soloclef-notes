"use client";

import { useQuery } from "@tanstack/react-query";
import { documentsApiService } from "@/lib/apis/documents-api.service";
import { cacheKeys } from "@/lib/query-client/cache-keys";

/**
 * How many documents the account has of each type, counted by the server
 * (GET /documents/type-counts). A document added, removed or retyped anywhere
 * refreshes it through the change stream (`documents`). The counts used to be
 * made in the browser from every document row synced to it.
 */
export function useDocumentTypeCounts(): Record<string, number> | undefined {
	const { data } = useQuery({
		queryKey: cacheKeys.documents.typeCounts(),
		queryFn: () => documentsApiService.getDocumentTypeCounts({}),
		// A count beside a connector card: a failed read shows none, not a toast.
		meta: { suppressGlobalErrorToast: true },
	});
	return data;
}
