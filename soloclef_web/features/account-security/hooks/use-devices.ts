"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { accountSecurityApi } from "../api/account-security-api";

/**
 * Everywhere the account is signed in. Read afresh on every visit: a phone
 * signed in or out a minute ago must show, and the list is one small read.
 */
export function useDevices() {
	return useQuery({
		queryKey: cacheKeys.user.devices(),
		queryFn: () => accountSecurityApi.devices(),
		staleTime: 0,
	});
}

/** Sign one other device out; the row goes when the list is read again. */
export function useSignOutDevice() {
	const queryClient = useQueryClient();
	return useMutation({
		meta: { suppressGlobalErrorToast: true },
		mutationFn: (sessionId: string) => accountSecurityApi.signOutDevice(sessionId),
		// A 404 means the sign-in had already ended (it expired, or another
		// device signed it out): the list is out of date either way.
		onSettled: () => queryClient.invalidateQueries({ queryKey: cacheKeys.user.devices() }),
	});
}
