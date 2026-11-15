/**
 * The dynamic pages of the static site, and how a page reads its value.
 *
 * Why this exists: `next build` writes the site as static files, and a
 * dynamic page such as /dashboard/notebooks/<id> cannot have a file for every
 * id. Each is written once, for the placeholder value below, and Caddy
 * answers every address under it with that one file
 * (docker/proxy-go/routes.caddy). The page's real value is then in the
 * address only: `useParams()` would say "_", the value it was written with,
 * so these pages read the address instead.
 */

/** The value each dynamic page is written for (generateStaticParams). */
export const STATIC_PLACEHOLDER = "_";

/** Every dynamic page: the address up to its value. routes.caddy matches the same prefixes. */
export const DYNAMIC_PREFIXES = {
	notebook: "/dashboard/notebooks/",
	automation: "/dashboard/automations/",
	publicLink: "/public/",
} as const;

/**
 * The address segment right after `prefix`, decoded; null when the address
 * is not under it, names nothing there, or is the placeholder itself (the
 * file being drawn at build time).
 */
export function segmentAfter(pathname: string | null | undefined, prefix: string): string | null {
	if (!pathname?.startsWith(prefix)) return null;
	const raw = pathname.slice(prefix.length).split("/")[0];
	if (!raw || raw === STATIC_PLACEHOLDER) return null;
	try {
		return decodeURIComponent(raw);
	} catch {
		// A malformed escape names nothing a link could have made.
		return null;
	}
}

/** A positive whole-number id after `prefix`, or null. */
export function idAfter(pathname: string | null | undefined, prefix: string): number | null {
	const segment = segmentAfter(pathname, prefix);
	if (!segment || !/^\d+$/.test(segment)) return null;
	const id = Number(segment);
	return Number.isSafeInteger(id) && id > 0 ? id : null;
}
