"use client";

import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useLocale } from "next-intl";
import { billingApi, type UsageQuery } from "../api/billing-api";
import { billingKeys } from "../api/billing-query-keys";

/** The signed-in person's plan, balances, payment options and catalog. */
export function useBillingMe(enabled = true) {
	const locale = useLocale();
	return useQuery({
		queryKey: billingKeys.me(locale),
		queryFn: ({ signal }) => billingApi.me(locale, signal),
		enabled,
		staleTime: 60_000,
		// A language switch keeps the old figures painted until the new ones land.
		placeholderData: keepPreviousData,
		retry: 1,
	});
}

/** Public plans and prices, for the pricing page (no sign-in). */
export function useBillingCatalog() {
	const locale = useLocale();
	return useQuery({
		queryKey: billingKeys.catalog(locale),
		queryFn: ({ signal }) => billingApi.catalog(locale, signal),
		staleTime: 5 * 60_000,
		placeholderData: keepPreviousData,
		retry: 1,
	});
}

export function useBillingUsage(query: UsageQuery) {
	const locale = useLocale();
	return useQuery({
		queryKey: billingKeys.usage(locale, query),
		queryFn: ({ signal }) => billingApi.usage(locale, query, signal),
		staleTime: 30_000,
		// Paging and filtering keep the current rows until the next page arrives.
		placeholderData: keepPreviousData,
	});
}

export function useBillingPayments() {
	const locale = useLocale();
	return useQuery({
		queryKey: billingKeys.payments(),
		queryFn: ({ signal }) => billingApi.payments(locale, signal),
		staleTime: 30_000,
		placeholderData: keepPreviousData,
	});
}
