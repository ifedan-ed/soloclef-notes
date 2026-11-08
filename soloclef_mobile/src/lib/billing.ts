/**
 * Refusals from the backend's billing gates (HTTP 402 with a billing code, or
 * a chat stream's error frame) and where to send the person: the web's
 * /billing page. The app sells nothing itself (store rules); it only links out.
 */
import { ApiError } from "./api/errors";
import { StreamError } from "./chat/stream-state";

export const BILLING_PATH = "/billing";

/** "credits": buy or wait for more; "plans": the plan does not include this. */
export type BillingAction = "credits" | "plans";

const CREDIT_CODES = new Set(["INSUFFICIENT_CREDITS", "PREMIUM_QUOTA_EXHAUSTED"]);

/** Which billing link a refusal calls for, or null when it is not a billing refusal. */
export function billingAction(code?: string | null, status?: number | null): BillingAction | null {
	if (code?.startsWith("PLAN_")) return "plans";
	if (code && CREDIT_CODES.has(code)) return "credits";
	if (status === 402) return "credits";
	return null;
}

/** The same, read off a thrown value (ApiError from a request, StreamError from chat). */
export function billingActionFor(error: unknown): BillingAction | null {
	if (error instanceof ApiError) return billingAction(error.code, error.status);
	if (error instanceof StreamError) return billingAction(error.errorCode);
	return null;
}

/** The message key for the link's label. */
export function billingActionLabelKey(action: BillingAction): "mobile.see_plans" | "mobile.get_credits" {
	return action === "plans" ? "mobile.see_plans" : "mobile.get_credits";
}

/** The web billing page on the same server the app signs in to. */
export function billingUrl(serverUrl: string): string {
	return `${serverUrl.replace(/\/+$/, "")}${BILLING_PATH}`;
}

/** Credits for display: whole numbers as is, fractions to at most two places. */
export function formatCredits(credits: number, locale = "en"): string {
	try {
		return new Intl.NumberFormat(locale, { maximumFractionDigits: 2 }).format(credits);
	} catch {
		return String(Math.round(credits * 100) / 100);
	}
}
