"use client";

import { useQuery } from "@tanstack/react-query";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { fetchSignInOptions } from "../api/sign-in-options-api";

/**
 * This server's sign-in options. They change only when the operator changes
 * the server's settings (the API lets a proxy keep them a minute), so one
 * read serves the whole visit. `enabled` false skips the read for a page
 * that turns out not to need it.
 */
export function useSignInOptions({ enabled = true }: { enabled?: boolean } = {}) {
	return useQuery({
		queryKey: cacheKeys.signInOptions(),
		queryFn: fetchSignInOptions,
		enabled,
		staleTime: Number.POSITIVE_INFINITY,
		// Only a page that needs them reads them, and each says what it does
		// without them, so one failed read is not retried in a loop.
		retry: 1,
	});
}
