import { atomWithMutation } from "jotai-tanstack-query";
import type { LoginRequest, RegisterRequest } from "@/contracts/types/auth.types";
import { authApiService } from "@/lib/apis/auth-api.service";
import { cacheKeys } from "@/lib/query-client/cache-keys";

// The sign-in and sign-up forms show their own error inline, worded for the
// person (lib/auth-errors.ts). The global mutation toast would add a second,
// raw one ("LOGIN_BAD_CREDENTIALS ... Report Issue") on top, so these two opt
// out of it.

export const registerMutationAtom = atomWithMutation(() => {
	return {
		mutationKey: cacheKeys.auth.user,
		mutationFn: async (request: RegisterRequest) => {
			return authApiService.register(request);
		},
		meta: { suppressGlobalErrorToast: true },
	};
});

export const loginMutationAtom = atomWithMutation(() => {
	return {
		mutationKey: cacheKeys.auth.user,
		mutationFn: async (request: LoginRequest) => {
			return authApiService.login(request);
		},
		meta: { suppressGlobalErrorToast: true },
	};
});
