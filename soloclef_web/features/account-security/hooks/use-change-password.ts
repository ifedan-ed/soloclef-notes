"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { accountSecurityApi } from "../api/account-security-api";

/**
 * Send a new password. On success the account has a password (if it had
 * none) and every other device is signed out, so both lists are read again.
 * Errors are the form's to word (lib/password-refusal), never a global toast.
 */
export function useChangePassword() {
	const queryClient = useQueryClient();
	return useMutation({
		meta: { suppressGlobalErrorToast: true },
		mutationFn: ({ current, next }: { current: string | null; next: string }) =>
			accountSecurityApi.changePassword(current, next),
		onSuccess: () => {
			void queryClient.invalidateQueries({ queryKey: cacheKeys.user.passwordState() });
			void queryClient.invalidateQueries({ queryKey: cacheKeys.user.devices() });
		},
	});
}
