/** Where every "you need credits or a bigger plan" message points. */
export const BILLING_PATH = "/billing";

/** Codes the server uses when billing refuses an action (HTTP 402). */
export const BILLING_REFUSAL_CODES = new Set([
	"INSUFFICIENT_CREDITS",
	"PREMIUM_QUOTA_EXHAUSTED",
	"PLAN_PARSER_NOT_INCLUDED",
	"PLAN_VISION_NOT_INCLUDED",
	"PLAN_MODEL_TIER_NOT_INCLUDED",
	"PLAN_STUDIO_KIND_NOT_INCLUDED",
	"PLAN_VIDEO_TOO_LONG",
	"PLAN_TOO_MANY_PAGES",
	"PLAN_TOO_MANY_JOBS",
	"PLAN_TOO_MANY_NOTEBOOKS",
	"PLAN_TOO_MANY_SOURCES",
]);

/** True for an error billing raised: a 402, or one of the refusal codes. */
export function isBillingRefusal(error: unknown): boolean {
	if (typeof error !== "object" || error === null) return false;
	const { status, code, errorCode } = error as {
		status?: unknown;
		code?: unknown;
		errorCode?: unknown;
	};
	if (status === 402) return true;
	return (
		(typeof code === "string" && BILLING_REFUSAL_CODES.has(code)) ||
		(typeof errorCode === "string" && BILLING_REFUSAL_CODES.has(errorCode))
	);
}

/** The billing settings tab for a workspace. */
export function billingSettingsPath(workspaceId: number | string): string {
	return `/dashboard/${workspaceId}/user-settings/billing`;
}

/** How someone came back from a payment page. */
export type ReturnFlag = "success" | "cancel" | "crypto_done" | null;

/**
 * Reads the return flag: Stripe Checkout sends `?checkout=success|cancel`,
 * BTCPay's checkout sends `?crypto=done`. Anything else is ignored.
 */
export function returnFlag(search: string | URLSearchParams): ReturnFlag {
	const params = typeof search === "string" ? new URLSearchParams(search) : search;
	const checkout = params.get("checkout");
	if (checkout === "success" || checkout === "cancel") return checkout;
	if (params.get("crypto") === "done") return "crypto_done";
	return null;
}

/** Where /billing sends someone: the workspace's billing tab, keeping the flag for a toast. */
export function billingReturnTarget(workspaceId: number | string, flag: ReturnFlag): string {
	const base = billingSettingsPath(workspaceId);
	if (flag === "crypto_done") return `${base}?crypto=done`;
	return flag ? `${base}?checkout=${flag}` : base;
}
