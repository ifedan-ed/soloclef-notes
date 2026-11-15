import { OAUTH_RESULT_COOKIE, type OAuthCallbackResult } from "@/contracts/types/oauth.types";

/**
 * The result a provider's OAuth flow ends with, as the API's callback hands
 * it over in the query string of /dashboard/connectors/callback.
 */
export function oauthResultFromQuery(search: string): OAuthCallbackResult {
	const params = new URLSearchParams(search);
	return {
		success: params.get("success"),
		error: params.get("error"),
		connector: params.get("connector"),
		connectorId: params.get("connectorId"),
	};
}

/**
 * The cookie that carries it to the connectors settings, where
 * `useConnectorDialog` reads it once and clears it. The same name, path,
 * minute and SameSite as when a Next.js route set it, so the reader did not
 * change; written here by the page because the site is static now.
 */
export function oauthResultCookie(result: OAuthCallbackResult): string {
	return `${OAUTH_RESULT_COOKIE}=${encodeURIComponent(JSON.stringify(result))}; path=/; max-age=60; samesite=lax`;
}
