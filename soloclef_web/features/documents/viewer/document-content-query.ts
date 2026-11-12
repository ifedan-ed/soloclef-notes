import { queryOptions } from "@tanstack/react-query";
import { z } from "zod";
import { authenticatedFetch } from "@/lib/auth-fetch";
import { buildBackendUrl } from "@/lib/env-config";

const DocumentContentSchema = z.object({
	document_id: z.number(),
	title: z.string(),
	document_type: z.string().optional(),
	source_markdown: z.string(),
	viewer_mode: z.enum(["plate", "monaco"]).optional(),
});

// Keyed by the document alone: a document id is the account's own or the
// route answers 404, so no second id is needed to say whose it is.
export function documentContentQueryOptions(documentId: number) {
	return queryOptions({
		queryKey: ["document-content", documentId] as const,
		queryFn: async () => {
			const response = await authenticatedFetch(
				buildBackendUrl(`/api/v1/documents/${documentId}/editor-content`),
				{ skipAuthRedirect: true }
			);
			if (!response.ok) {
				const body = await response.json().catch(() => null);
				throw new Error(body?.detail ?? `Could not load document (${response.status})`);
			}
			return DocumentContentSchema.parse(await response.json());
		},
		staleTime: 30_000,
	});
}
