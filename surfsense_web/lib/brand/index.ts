/**
 * The product's name, colours and logo paths, in one place.
 *
 * `brand.json` is the single source: the backend keeps a Python copy in
 * `surfsense_backend/app/brand.py`, and a backend test fails if the two drift.
 * Internal identifiers (package names, env vars, cookie and storage keys) keep
 * their upstream names on purpose, so upstream merges stay manageable; only
 * what a person sees reads from here.
 *
 * The name, colours and logo are the product's. The site's address and its
 * contact address are the server's own, so `brand.json` leaves them empty and
 * each deployment sets them at build time (the public pages are prerendered):
 * `NEXT_PUBLIC_SITE_URL` (docker-compose.yml passes SURFSENSE_PUBLIC_URL) and
 * `NEXT_PUBLIC_SUPPORT_EMAIL` (SUPPORT_EMAIL). Unset, the site prints no
 * absolute links (no sitemap entries, canonical or share-card URL) and no
 * address. `NEXT_PUBLIC_BRAND_NAME` renames the product for a second
 * deployment under another name.
 */
import brandJson from "./brand.json";

export interface Brand {
	name: string;
	shortName: string;
	family: string;
	tagline: string;
	description: string;
	siteUrl: string;
	contactEmail: string;
	colors: {
		ink: string;
		accent: string;
		accentOnDark: string;
		background: string;
		backgroundDark: string;
	};
	logo: {
		mark: string;
		markMono: string;
		wordmark: string;
		icon192: string;
		icon512: string;
		maskable512: string;
		og: string;
	};
}

function nonEmpty(value: string | undefined): string | undefined {
	const trimmed = value?.trim();
	return trimmed ? trimmed : undefined;
}

export function resolveBrand(
	base: Brand,
	env: { name?: string; siteUrl?: string; contactEmail?: string } = {}
): Brand {
	const siteUrl = (nonEmpty(env.siteUrl) ?? base.siteUrl).trim().replace(/\/+$/, "");
	return {
		...base,
		name: nonEmpty(env.name) ?? base.name,
		siteUrl,
		contactEmail: nonEmpty(env.contactEmail) ?? base.contactEmail,
	};
}

// Each NEXT_PUBLIC_* is spelled out so `next build` inlines it.
export const BRAND: Brand = resolveBrand(brandJson as Brand, {
	name: process.env.NEXT_PUBLIC_BRAND_NAME,
	siteUrl: process.env.NEXT_PUBLIC_SITE_URL,
	contactEmail: process.env.NEXT_PUBLIC_SUPPORT_EMAIL,
});

/** "Pricing" -> "Pricing · Soloclef Notes"; no page -> the name alone. */
export function brandTitle(page?: string, brand: Brand = BRAND): string {
	const trimmed = page?.trim();
	return trimmed ? `${trimmed} · ${brand.name}` : brand.name;
}

/**
 * An absolute URL on the public site, for canonical links and sitemaps;
 * undefined when the server has no address configured, so metadata leaves the
 * field out instead of naming some other host.
 */
export function siteUrl(path = "/", brand: Brand = BRAND): string | undefined {
	if (!brand.siteUrl) return undefined;
	const clean = path.startsWith("/") ? path : `/${path}`;
	return clean === "/" ? `${brand.siteUrl}/` : `${brand.siteUrl}${clean}`;
}

/** Next's `metadataBase`, which resolves the share image's path; none without an address. */
export function metadataBase(brand: Brand = BRAND): URL | undefined {
	return brand.siteUrl ? new URL(brand.siteUrl) : undefined;
}

/** The share image, for a page's own openGraph block (Next replaces the root one). */
export const OG_IMAGES = [{ url: BRAND.logo.og, width: 1200, height: 630, alt: BRAND.name }];
