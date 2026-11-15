/**
 * The new-password rules, checked here only so a person is told at once what
 * to fix, without a request. The server decides (soloclef_api/internal/signin,
 * checkNewPassword); these are its numbers, and docs/limits.md lists them.
 */

/** A new password's fewest characters (signin.minPasswordLength). */
export const PASSWORD_MIN = 8;
/** And its most (signin.maxPasswordLength). */
export const PASSWORD_MAX = 256;

/** What is wrong with the form as filled, as a message key in `account_security`. */
export type PasswordProblem =
	| "enter_current"
	| "too_short"
	| "too_long"
	| "same_as_email"
	| "mismatch";

/**
 * The first thing to fix before the form may be sent, or null. `current` is
 * null for an account with no password, which is not asked for one.
 */
export function passwordFormProblem(
	current: string | null,
	next: string,
	again: string,
	email: string | undefined
): PasswordProblem | null {
	if (current !== null && current === "") return "enter_current";
	// Characters as the server counts them (code points), not UTF-16 units:
	// an emoji is one character there, and two here with `.length`.
	const length = [...next].length;
	if (length < PASSWORD_MIN) return "too_short";
	if (length > PASSWORD_MAX) return "too_long";
	if (email && next.trim().toLowerCase() === email.trim().toLowerCase()) return "same_as_email";
	if (next !== again) return "mismatch";
	return null;
}
