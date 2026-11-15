/**
 * Replaces the address's query in place, for a page that drops a one-time
 * marker from it (`?google_drive=connected` after a sign-in comes back).
 *
 * Why not `router.replace(pathname + query)`: the site is a static export,
 * whose pages start the router knowing their address without its query, so
 * replacing it with that same address was taken for no move at all and the
 * marker stayed. Nothing about the page changes but its query, so there is
 * nothing to fetch either: the History API changes the address, and Next.js
 * follows it (useSearchParams sees the new query).
 */
export function replaceAddressQuery(query: string): void {
	const search = query && !query.startsWith("?") ? `?${query}` : query;
	window.history.replaceState(
		null,
		"",
		`${window.location.pathname}${search}${window.location.hash}`
	);
}
