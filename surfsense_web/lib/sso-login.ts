import { buildBackendUrl } from "@/lib/env-config";

/**
 * Where the single sign-on button sends the browser. `returnTo` is the page
 * the person was sent away from; the backend only honours a path on this
 * site and otherwise lands them on the dashboard.
 */
export function buildSsoStartUrl(returnTo?: string | null): string {
	const next = returnTo?.startsWith("/") && !returnTo.startsWith("//") ? returnTo : null;
	return buildBackendUrl("/auth/oidc/authorize-redirect", { next });
}
