import { settingsPath } from "@/lib/dashboard-paths";
import { buildSsoStartUrl } from "@/lib/sso-login";

/**
 * Confirming an account made by single sign-on before it sets its first
 * password. The server takes the new password only from a sign-in its
 * provider proved in the last ten minutes. The phone app confirms its
 * existing sign-in in a browser sheet; a website can only send the whole page
 * through the provider, which signs this browser in afresh (and the API ends
 * the sign-in it replaces), then brings it back here.
 *
 * The password itself is never carried across: it would have to sit in the
 * address or in storage while the page is away. The person types it after
 * coming back, and the page says they are confirmed.
 */

/** The query parameter that says "back from confirming". */
export const CONFIRMED_PARAM = "confirmed";
const CONFIRMED_VALUE = "sso";

/** The Security page, marked as reached by coming back from the provider. */
export function confirmedReturnPath(): string {
	return `${settingsPath("security")}?${CONFIRMED_PARAM}=${CONFIRMED_VALUE}`;
}

/** Where the Confirm button sends the browser. */
export function ssoConfirmationUrl(): string {
	return buildSsoStartUrl(confirmedReturnPath());
}

/** Whether the page was opened on the way back from the provider. */
export function cameBackConfirmed(search: URLSearchParams | null): boolean {
	return search?.get(CONFIRMED_PARAM) === CONFIRMED_VALUE;
}

/**
 * How long a confirmation lets a first password be set: the server's
 * ssoProofWindow (soloclef_api/internal/signin, lifetimes.go). Shown to the
 * person; the server enforces it.
 */
export const SSO_PROOF_MINUTES = 10;
