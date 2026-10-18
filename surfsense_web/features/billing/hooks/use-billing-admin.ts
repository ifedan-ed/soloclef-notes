"use client";

import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslations } from "next-intl";
import { toast } from "sonner";
import { billingAdminApi } from "../api/billing-admin-api";
import { billingKeys } from "../api/billing-query-keys";

export function useAdminOverview(enabled = true) {
	return useQuery({
		queryKey: billingKeys.admin.overview(),
		queryFn: ({ signal }) => billingAdminApi.overview(signal),
		enabled,
		staleTime: 30_000,
	});
}

export function useAdminUsers(q: string, offset: number, limit: number) {
	return useQuery({
		queryKey: billingKeys.admin.users(q, offset),
		queryFn: ({ signal }) => billingAdminApi.users({ q: q || undefined, offset, limit }, signal),
		staleTime: 15_000,
		placeholderData: keepPreviousData,
	});
}

export function useAdminLedger(userId: string | null) {
	return useQuery({
		queryKey: billingKeys.admin.ledger(userId ?? ""),
		queryFn: ({ signal }) => billingAdminApi.ledger(userId ?? "", signal),
		enabled: !!userId,
		staleTime: 15_000,
	});
}

export function useAdminModels() {
	return useQuery({
		queryKey: billingKeys.admin.models(),
		queryFn: ({ signal }) => billingAdminApi.models(signal),
		staleTime: 60_000,
	});
}

export function useAdminReport(days: number) {
	return useQuery({
		queryKey: billingKeys.admin.report(days),
		queryFn: ({ signal }) => billingAdminApi.report(days, signal),
		staleTime: 60_000,
		placeholderData: keepPreviousData,
	});
}

/**
 * A billing-admin write: toasts the outcome and refreshes every billing query
 * (the catalog the admin edits is the one users see).
 */
export function useAdminMutation<TArgs, TResult>(
	fn: (args: TArgs) => Promise<TResult>,
	options: { success?: string; onSuccess?: (result: TResult) => void } = {}
) {
	const t = useTranslations("billing");
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: fn,
		onSuccess: async (result) => {
			await queryClient.invalidateQueries({ queryKey: billingKeys.all });
			toast.success(options.success ?? t("admin.saved"));
			options.onSuccess?.(result);
		},
		onError: (error) =>
			toast.error(error instanceof Error && error.message ? error.message : t("errors.generic")),
	});
}
