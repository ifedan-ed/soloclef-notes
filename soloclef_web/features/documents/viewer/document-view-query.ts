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
		staleTime: 30_000,
		refetchInterval: (query) => {
			const manifest = query.state.data;
			return manifest?.presentation === "missing_original" &&
				(manifest.status === "pending" || manifest.status === "processing")
				? 2_000
				: false;
		},
	});
}
