import { useQuery } from "@tanstack/react-query";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { publicOutputApi } from "../api/public-output-api";
import type { PublicOutputPage } from "../model/public-output";

/**
 * The read behind a public link's page. Not retried: a 404 or 410 is the
 * answer (revoked or expired), and the page says so at once. Every failure
 * is the page's to show (PublicLinkUnavailable), so the global error toast
 * stays out of it: a visitor to a revoked link saw "Error: NOT_FOUND" and
 * Report Issue on top of the page that already says the link has gone.
 */
export function publicOutputQuery(shareToken: string) {
	return {
		queryKey: cacheKeys.publicOutput.byToken(shareToken),
		queryFn: () => publicOutputApi.page(shareToken),
		enabled: !!shareToken,
		staleTime: 30_000,
		retry: false,
		meta: { suppressGlobalErrorToast: true },
	} as const;
}

/** The shared output a public link names. */
export function usePublicOutput(shareToken: string) {
	return useQuery<PublicOutputPage, Error>(publicOutputQuery(shareToken));
}
