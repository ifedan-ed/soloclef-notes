"use client";

import { useQuery } from "@tanstack/react-query";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { accountSecurityApi } from "../api/account-security-api";

/**
 * Whether the account has a password, which decides the form: the current
 * password is asked for only when there is one. Read afresh on every visit
 * (staleTime 0), as the phone app does: the answer changes the moment a
 * first password is set anywhere, and a stale "no password" would send a
 * change without the current one, which the server refuses.
 */
export function usePasswordState() {
	return useQuery({
		queryKey: cacheKeys.user.passwordState(),
		queryFn: () => accountSecurityApi.passwordState(),
		staleTime: 0,
	});
}
