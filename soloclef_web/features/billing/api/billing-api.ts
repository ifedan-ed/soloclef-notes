import { baseApiService } from "@/lib/apis/base-api.service";
import {
	billingMeSchema,
	type CheckoutRequest,
	type CryptoPaymentRequest,
	catalogSchema,
	okSchema,
	paymentListSchema,
	paymentSchema,
	urlSchema,
	usagePageSchema,
} from "./billing-schemas";

const BASE = "/api/v1/billing";

/**
 * Request options that carry the UI locale, so plan names, features, labels and
 * pack names come back in the language on screen. The Content-Type is repeated
 * because passing `headers` replaces the service's defaults.
 */
export function localeOptions(locale: string, signal?: AbortSignal) {
	return {
		headers: { "Content-Type": "application/json", "X-Locale": locale },
		signal,
	};
}

export type UsageQuery = {
	limit: number;
	offset: number;
	action?: string;
	notebookId?: number;
	days?: number;
};

export function usageSearch(query: UsageQuery): string {
	const params = new URLSearchParams({ limit: String(query.limit), offset: String(query.offset) });
	if (query.action) params.set("action", query.action);
	if (query.notebookId !== undefined) params.set("notebook_id", String(query.notebookId));
	if (query.days !== undefined) params.set("days", String(query.days));
	return params.toString();
}

/** A signed-in person's own billing. No call takes a user id. */
export const billingApi = {
	catalog: (locale: string, signal?: AbortSignal) =>
		baseApiService.get(`${BASE}/catalog`, catalogSchema, localeOptions(locale, signal)),
	me: (locale: string, signal?: AbortSignal) =>
		baseApiService.get(`${BASE}/me`, billingMeSchema, localeOptions(locale, signal)),
	usage: (locale: string, query: UsageQuery, signal?: AbortSignal) =>
		baseApiService.get(
			`${BASE}/usage?${usageSearch(query)}`,
			usagePageSchema,
			localeOptions(locale, signal)
		),
	payments: (locale: string, signal?: AbortSignal) =>
		baseApiService.get(`${BASE}/payments`, paymentListSchema, localeOptions(locale, signal)),
	checkout: (locale: string, body: CheckoutRequest) =>
		baseApiService.post(`${BASE}/stripe/checkout`, urlSchema, { ...localeOptions(locale), body }),
	portal: (locale: string) =>
		baseApiService.post(`${BASE}/stripe/portal`, urlSchema, localeOptions(locale)),
	changePlan: (locale: string, planId: number) =>
		baseApiService.post(`${BASE}/stripe/change-plan`, okSchema, {
			...localeOptions(locale),
			body: { plan_id: planId },
		}),
	cancel: (locale: string, cancel: boolean) =>
		baseApiService.post(`${BASE}/stripe/cancel`, okSchema, {
			...localeOptions(locale),
			body: { cancel },
		}),
	stripeReturn: (locale: string, sessionId: string) =>
		baseApiService.get(
			`${BASE}/stripe/return?${new URLSearchParams({ session_id: sessionId })}`,
			paymentSchema,
			localeOptions(locale)
		),
	createCryptoPayment: (locale: string, body: CryptoPaymentRequest) =>
		baseApiService.post(`${BASE}/crypto/payments`, paymentSchema, {
			...localeOptions(locale),
			body,
		}),
};
