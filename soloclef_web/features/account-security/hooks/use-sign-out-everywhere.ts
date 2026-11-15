"use client";

import { useMutation } from "@tanstack/react-query";
import { accountSecurityApi } from "../api/account-security-api";
import { leaveToSignIn } from "../lib/leave-to-sign-in";

/**
 * Sign every device out, this browser included. The server ends every
 * sign-in and clears this browser's cookies, so the page leaves for the
 * sign-in page straight away rather than at its next request.
 */
export function useSignOutEverywhere() {
	return useMutation({
		meta: { suppressGlobalErrorToast: true },
		mutationFn: () => accountSecurityApi.signOutEverywhere(),
		onSuccess: () => leaveToSignIn(),
	});
}
