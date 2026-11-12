import { baseApiService } from "@/lib/apis/base-api.service";

// Restated because post()'s default headers are replaced by any given.
const JSON_HEADERS = { "Content-Type": "application/json" };

/** Ask for a reset email. The answer is the same whether or not the address has an account. */
export async function requestPasswordReset(email: string) {
	await baseApiService.post("/auth/forgot-password", undefined, {
		body: { email },
		headers: JSON_HEADERS,
	});
}

/** Set a new password with the token from the email link. */
export async function resetPassword(token: string, password: string) {
	await baseApiService.post("/auth/reset-password", undefined, {
		body: { token, password },
		headers: JSON_HEADERS,
	});
}
