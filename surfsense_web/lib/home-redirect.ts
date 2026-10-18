/**
 * Where "/" sends someone who is already signed in.
 *
 * Web auth is an httpOnly cookie pair set by the backend on this same origin,
 * so the edge proxy can see whether one exists without reading it. Presence is
 * enough: the dashboard verifies it and sends an expired session to /login.
 * The names follow the backend's SESSION_COOKIE_NAME / REFRESH_COOKIE_NAME
 * defaults.
 */
export const SESSION_COOKIE_NAMES = ["surfsense_session", "surfsense_refresh"] as const;

export function signedInHomeRedirect(
	pathname: string,
	hasCookie: (name: string) => boolean
): string | null {
	if (pathname !== "/") return null;
	return SESSION_COOKIE_NAMES.some((name) => hasCookie(name)) ? "/dashboard" : null;
}
