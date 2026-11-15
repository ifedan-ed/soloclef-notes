import { AppError } from "@/lib/error";

/**
 * The server's refusals of a password change (password_change.go), which the
 * API names in the error's message.
 */
export const BAD_CURRENT = "CHANGE_PASSWORD_BAD_CURRENT";
export const INVALID_PASSWORD = "CHANGE_PASSWORD_INVALID_PASSWORD";
export const CONFIRM_SSO = "CHANGE_PASSWORD_CONFIRM_SSO";

/** A refusal, as a message key in `account_security`. */
export type PasswordRefusal =
	| "bad_current"
	| "invalid_password"
	| "too_many_tries"
	| "change_failed";

/**
 * Whether the server wants the person confirmed through single sign-on
 * first: an account with no password, asked from a sign-in the provider did
 * not prove in the last ten minutes.
 */
export function needsSsoConfirmation(error: unknown): boolean {
	return error instanceof AppError && error.status === 403 && error.message === CONFIRM_SSO;
}

/** What to tell the person about a refused change. */
export function passwordRefusal(error: unknown): PasswordRefusal {
	if (!(error instanceof AppError)) return "change_failed";
	if (error.message === BAD_CURRENT) return "bad_current";
	if (error.message === INVALID_PASSWORD) return "invalid_password";
	// The route shares the sign-in form's per-client limit.
	if (error.status === 429) return "too_many_tries";
	return "change_failed";
}
