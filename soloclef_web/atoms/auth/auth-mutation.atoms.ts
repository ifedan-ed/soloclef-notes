import { atomWithMutation } from "jotai-tanstack-query";
import type { LoginRequest, RegisterRequest } from "@/contracts/types/auth.types";
import { authApiService } from "@/lib/apis/auth-api.service";
import { cacheKeys } from "@/lib/query-client/cache-keys";

export const registerMutationAtom = atomWithMutation(() => {
	return {
		mutationKey: cacheKeys.auth.user,
		mutationFn: async ({
			humanCheckToken,
			...request
		}: RegisterRequest & { humanCheckToken?: string | null }) => {
			return authApiService.register(request, humanCheckToken);
		},
	};
});

export const loginMutationAtom = atomWithMutation(() => {
	return {
		mutationKey: cacheKeys.auth.user,
		mutationFn: async ({
			humanCheckToken,
			...request
		}: LoginRequest & { humanCheckToken?: string | null }) => {
			return authApiService.login(request, humanCheckToken);
		},
	};
});
