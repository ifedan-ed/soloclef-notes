/**
 * Which routes are reachable without a session.
 *
 * Kept apart from `auth-utils` so the edge middleware can import the predicate
 * without dragging in helpers that touch `window` and `localStorage`. The auth
 * redirect relies on it.
 */

/** Path prefixes for routes that do not require auth (no current-user fetch, no redirect on 401) */
const PUBLIC_ROUTE_PREFIXES = [
	"/login",
	"/register",
	"/forgot-password",
	"/reset-password",
	"/auth",
	"/desktop/login",
	"/docs",
	"/public",
	"/invite",
	// The public site. A marketing route missing from this list still renders,
	// but only on the client: `ZeroProvider` mounts the authenticated sync
	// provider for it, which emits no SSR HTML, so a crawler sees an empty shell.
	"/features",
	"/pricing",
	"/contact",
	"/privacy",
	"/terms",
];

/**
 * Returns true if the pathname is a public route where we should not run auth checks
 * or redirect to login on 401.
 */
export function isPublicRoute(pathname: string): boolean {
	if (pathname === "/" || pathname === "") return true;
	return PUBLIC_ROUTE_PREFIXES.some((prefix) => pathname.startsWith(prefix));
}
