import type { Payment } from "../api/billing-schemas";

/**
 * Crypto payments run on the owner's BTCPay Server: the app creates the
 * invoice, then sends the buyer to BTCPay's hosted checkout (`payment_url`),
 * which returns them to `/billing?crypto=done`.
 */

/** The method select's value for "let the buyer choose on BTCPay's checkout". */
export const ANY_CRYPTO_METHOD = "any";

/** A payment that can still be paid on its checkout page. */
export function isOpenPayment(status: string): boolean {
	return status === "pending" || status === "partial";
}

/** The checkout link for an open crypto payment, if it has one. */
export function continuePaymentUrl(
	payment: Pick<Payment, "provider" | "status" | "crypto">
): string | null {
	if (payment.provider === "stripe" || !isOpenPayment(payment.status)) return null;
	return payment.crypto?.payment_url || null;
}

/** "BTC-CHAIN" -> "btc_chain": a message key for a BTCPay payment method id. */
export function methodMessageKey(method: string): string {
	return method.toLowerCase().replace(/[^a-z0-9]+/g, "_");
}

/** The request field for the chosen method: omitted for "any". */
export function payCurrencyFor(method: string): string | undefined {
	return method === ANY_CRYPTO_METHOD || !method ? undefined : method;
}

/** True for the 503 BTCPay answers with when it is too busy to open a checkout. */
export function isCryptoBusy(error: unknown): boolean {
	return (
		typeof error === "object" &&
		error !== null &&
		(error as { code?: unknown }).code === "CRYPTO_BUSY"
	);
}
