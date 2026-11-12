import { AppError } from "@/lib/error";

/** What went wrong with a reset or forgot request, as a message key in `auth`. */
export type ResetProblem =
	| "reset_link_invalid"
	| "password_too_short"
	| "too_many_attempts"
	| "reset_failed";

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
