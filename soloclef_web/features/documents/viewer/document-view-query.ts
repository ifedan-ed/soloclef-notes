import { queryOptions } from "@tanstack/react-query";
import { authenticatedFetch } from "@/lib/auth-fetch";
import { buildBackendUrl } from "@/lib/env-config";
import { DocumentViewManifestSchema } from "./model";

export const documentViewQueryKey = (documentId: number) => ["document-view", documentId] as const;

export function documentViewQueryOptions(documentId: number) {
	return queryOptions({
		queryKey: documentViewQueryKey(documentId),
		queryFn: async () => {
			const response = await authenticatedFetch(
				buildBackendUrl(`/api/v1/documents/${documentId}/view-manifest`),
				{ skipAuthRedirect: true }
			);
			if (!response.ok) {
				throw new Error(`Could not load document (${response.status})`);
			}
			return DocumentViewManifestSchema.parse(await response.json());
		},
		// No polling while the document is still being indexed: the server
		// reports every status write on the change stream (`documents`), which
		// reads this manifest again (features/live-changes).
		staleTime: 30_000,
	});
}
