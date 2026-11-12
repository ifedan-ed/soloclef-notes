"use client";

import { useMutation, useQuery } from "@tanstack/react-query";
import type { UpdateUserSettingsRequest, UserSettings } from "@/contracts/types/user.types";
import { userApiService } from "@/lib/apis/user-api.service";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { queryClient } from "@/lib/query-client/client";

/**
 * The account's settings, shared by every place that shows one of them (the
 * settings sections, the chat's "instructions applied" mark), so a save in
 * one shows in the others without a reload.
 */
export function useAccountSettings() {
	return useQuery({
		queryKey: cacheKeys.user.settings(),
		queryFn: () => userApiService.getSettings(),
		staleTime: 60_000,
	});
}

/** Change any of the settings. The answer is all of them as they now stand, cached as is. */
export function useUpdateAccountSettings() {
	return useMutation({
		mutationFn: (request: UpdateUserSettingsRequest) => userApiService.updateSettings(request),
		onSuccess: (settings: UserSettings) => {
			queryClient.setQueryData(cacheKeys.user.settings(), settings);
		},
	});
}
