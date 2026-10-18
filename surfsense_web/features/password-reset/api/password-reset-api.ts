import { humanCheckHeaders } from "@/features/human-check/lib/turnstile-script";
import { baseApiService } from "@/lib/apis/base-api.service";

function jsonHeaders(humanCheckToken?: string | null): Record<string, string> {
	return { "Content-Type": "application/json", ...humanCheckHeaders(humanCheckToken) };
}

/** Ask for a reset email. The answer is the same whether or not the address has an account. */
export async function requestPasswordReset(email: string, humanCheckToken?: string | null) {
	await baseApiService.post("/auth/forgot-password", undefined, {
		body: { email },
		headers: jsonHeaders(humanCheckToken),
	});
}

/** Set a new password with the token from the email link. */
export async function resetPassword(
	token: string,
	password: string,
	humanCheckToken?: string | null
) {
	await baseApiService.post("/auth/reset-password", undefined, {
		body: { token, password },
		headers: jsonHeaders(humanCheckToken),
	});
}
