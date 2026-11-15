import { AppError } from "@/lib/error";

/** What went wrong with a reset or forgot request, as a message key in `auth`. */
export type ResetProblem =
	| "reset_link_invalid"
	| "password_too_short"
	| "too_many_attempts"
	| "reset_unavailable"
	| "reset_failed";

/**
 * How long a mailed reset link works, in hours: the API's resetLifetime
 * (soloclef_api/internal/signin, password_reset.go). The confirmation says
 * it, so it must say what the server does.
 */
export const RESET_LINK_LIFETIME_HOURS = 1;

/** The API's refusal of a reset on a server that sends no mail. */
export const RESET_UNAVAILABLE = "PASSWORD_RESET_UNAVAILABLE";

export const MIN_PASSWORD_LENGTH = 8;

export function classifyResetError(error: unknown): ResetProblem {
	const message = error instanceof Error ? error.message : "";
	const code = error instanceof AppError ? (error.code ?? "") : "";
	const status = error instanceof AppError ? error.status : undefined;
	if (message === "RESET_PASSWORD_BAD_TOKEN") return "reset_link_invalid";
	// The API names the reason in the message; `code` only restates the status.
	if (message === "RESET_PASSWORD_INVALID_PASSWORD" || code === "RESET_PASSWORD_INVALID_PASSWORD") {
		return "password_too_short";
	}
	if (status === 429 || message === "RATE_LIMIT_EXCEEDED") return "too_many_attempts";
	// Normally the page knows beforehand (sign-in options) and never sends
	// the form; this covers a server whose mail was switched off since.
	if (message === RESET_UNAVAILABLE) return "reset_unavailable";
	return "reset_failed";
}

/** A client-side check before asking the server, so the obvious case is instant. */
export function newPasswordProblem(
	password: string,
	confirm: string
): ResetProblem | "password_mismatch" | null {
	if (password.length < MIN_PASSWORD_LENGTH) return "password_too_short";
	if (password !== confirm) return "password_mismatch";
	return null;
}
