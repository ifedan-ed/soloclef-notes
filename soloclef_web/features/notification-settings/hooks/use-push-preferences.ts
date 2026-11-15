"use client";

import { useMutation, useQuery } from "@tanstack/react-query";
import { useTranslations } from "next-intl";
import { toast } from "sonner";
import type {
	PushPreferences,
	PushPreferencesChange,
} from "@/contracts/types/push-preferences.types";
import { pushPreferencesApiService } from "@/lib/apis/push-preferences-api.service";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { queryClient } from "@/lib/query-client/client";
import { withChange, withoutChange } from "../lib/preference-change";

const SAVE_KEY = ["user", "push-preferences", "save"] as const;

export function usePushPreferences() {
	return useQuery({
		queryKey: cacheKeys.user.pushPreferences(),
		queryFn: () => pushPreferencesApiService.get(),
		staleTime: 60_000,
	});
}

/**
 * Save a change at once: the page shows it before the server answers, and
 * a refusal puts back what that change touched, with the server's reason in
 * a toast (the phone app's switches behave the same).
 */
export function useSavePushPreferences() {
	const t = useTranslations("notification_settings");
	const key = cacheKeys.user.pushPreferences();
	return useMutation({
		mutationKey: SAVE_KEY,
		mutationFn: (change: PushPreferencesChange) => pushPreferencesApiService.update(change),
		onMutate: async (change) => {
			// A read still on its way would land over the change shown here.
			await queryClient.cancelQueries({ queryKey: key });
			const before = queryClient.getQueryData<PushPreferences>(key);
			if (before) queryClient.setQueryData(key, withChange(before, change));
			return { before };
		},
		onError: (error, change, context) => {
			const before = context?.before;
			if (before) {
				queryClient.setQueryData<PushPreferences>(key, (current) =>
					current ? withoutChange(current, change, before) : before
				);
			}
			toast.error(error instanceof Error && error.message ? error.message : t("save_failed"));
		},
		onSuccess: (saved) => {
			// With another save still in flight, this answer predates it and
			// would flip that switch back for a moment; the last save's answer
			// holds every change, so only it is written.
			if (queryClient.isMutating({ mutationKey: SAVE_KEY }) <= 1) {
				queryClient.setQueryData(key, saved);
			}
		},
	});
}
