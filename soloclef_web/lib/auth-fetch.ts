import { handleUnauthorized, refreshSession } from "@/lib/auth-utils";

type AuthenticatedFetchOptions = RequestInit & {
	skipAuthRedirect?: boolean;
};

// The session is a cookie, so a request only has to send credentials; no
// token is read or attached here.
function fetchWithSession(url: string, options: RequestInit): Promise<Response> {
	return fetch(url, { ...options, credentials: options.credentials ?? "include" });
}

/**
 * fetch with the session cookie. A 401 refreshes the session once and
 * retries; if that fails too the person is sent to sign in, unless
 * skipAuthRedirect asks for the 401 back instead.
 */
export async function authenticatedFetch(
	url: string,
	options: AuthenticatedFetchOptions = {}
): Promise<Response> {
	const { skipAuthRedirect = false, ...fetchOptions } = options;

	const response = await fetchWithSession(url, fetchOptions);
	if (response.status !== 401) {
		return response;
	}

	let unauthorizedResponse = response;
	if (await refreshSession()) {
		const retryResponse = await fetchWithSession(url, fetchOptions);
		if (retryResponse.status !== 401) {
			return retryResponse;
		}
		unauthorizedResponse = retryResponse;
	}

	if (!skipAuthRedirect) {
		handleUnauthorized();
		throw new Error("Unauthorized: Redirecting to login page");
	}

	return unauthorizedResponse;
}
