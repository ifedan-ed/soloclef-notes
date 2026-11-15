import { useQuery } from "@tanstack/react-query";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { publicOutputApi } from "../api/public-output-api";
import type { PublicOutputPage } from "../model/public-output";

/**
 * The shared output a public link names. Not retried: a 404 or 410 is the
 * answer (revoked or expired), and the page says so at once.
 */
export function usePublicOutput(shareToken: string) {
	return useQuery<PublicOutputPage, Error>({
		queryKey: cacheKeys.publicOutput.byToken(shareToken),
		queryFn: () => publicOutputApi.page(shareToken),
		enabled: !!shareToken,
		staleTime: 30_000,
		retry: false,
	});
}
