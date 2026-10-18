"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useLocale, useTranslations } from "next-intl";
import { toast } from "sonner";
import { billingApi } from "../api/billing-api";
import { billingKeys } from "../api/billing-query-keys";
import type { CheckoutRequest, CryptoPaymentRequest } from "../api/billing-schemas";
import { isCryptoBusy } from "../lib/crypto-payment";

function errorMessage(error: unknown, fallback: string): string {
	return error instanceof Error && error.message ? error.message : fallback;
}

/**
 * Buying and managing a subscription. Stripe and BTCPay pages are reached by a
 * full-page redirect (not a popup); everything else refreshes the billing queries.
 */
export function useBillingActions() {
	const t = useTranslations("billing");
	const locale = useLocale();
	const queryClient = useQueryClient();
	const refresh = () => queryClient.invalidateQueries({ queryKey: billingKeys.all });
	const onError = (error: unknown) => toast.error(errorMessage(error, t("errors.generic")));

	const checkout = useMutation({
		mutationFn: (body: CheckoutRequest) => billingApi.checkout(locale, body),
		onSuccess: ({ url }) => window.location.assign(url),
		onError,
	});
	const portal = useMutation({
		mutationFn: () => billingApi.portal(locale),
		onSuccess: ({ url }) => window.location.assign(url),
		onError,
	});
	const changePlan = useMutation({
		mutationFn: (planId: number) => billingApi.changePlan(locale, planId),
		onSuccess: async () => {
			await refresh();
			toast.success(t("toasts.plan_changed"));
		},
		onError,
	});
	const cancel = useMutation({
		mutationFn: (value: boolean) => billingApi.cancel(locale, value),
		onSuccess: async (_data, value) => {
			await refresh();
			toast.success(value ? t("toasts.cancelled") : t("toasts.resumed"));
		},
		onError,
	});
	// The invoice is created here, then paid on BTCPay's hosted checkout.
	const createCrypto = useMutation({
		mutationFn: (body: CryptoPaymentRequest) => billingApi.createCryptoPayment(locale, body),
		onSuccess: (payment) => {
			void queryClient.invalidateQueries({ queryKey: billingKeys.payments() });
			const url = payment.crypto?.payment_url;
			if (url) window.location.assign(url);
			else toast.error(t("errors.generic"));
		},
		// A busy checkout is explained inline in the purchase dialog.
		onError: (error) => (isCryptoBusy(error) ? undefined : onError(error)),
	});

	return { checkout, portal, changePlan, cancel, createCrypto, refresh };
}
