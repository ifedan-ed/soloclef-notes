import { z } from "zod";
import { baseApiService } from "@/lib/apis/base-api.service";

/** The route's own limit (soloclef_api/internal/citedsources): more ids are asked in batches. */
export const MAX_IDS_PER_REQUEST = 100;

const citedPassageSchema = z.object({
	chunk_id: z.number(),
	document_id: z.number(),
	title: z.string(),
	page: z.number().nullable(),
});

const citedSourcesSchema = z.object({ sources: z.array(citedPassageSchema) });

export type CitedPassage = z.infer<typeof citedPassageSchema>;

/**
 * The documents a set of cited passages are in, with each passage's page:
 * one light request for a whole answer's sources (GET /api/v1/chunks/sources).
 * Passages the account cannot see, or that are gone, are simply absent.
 */
export async function fetchCitedPassages(chunkIds: readonly number[]): Promise<CitedPassage[]> {
	const found: CitedPassage[] = [];
	for (let at = 0; at < chunkIds.length; at += MAX_IDS_PER_REQUEST) {
		const ids = chunkIds.slice(at, at + MAX_IDS_PER_REQUEST).join(",");
		const page = await baseApiService.get(`/api/v1/chunks/sources?ids=${ids}`, citedSourcesSchema);
		found.push(...page.sources);
	}
	return found;
}
