import type { MetadataRoute } from "next";
import { BRAND, type Brand, siteUrl } from "@/lib/brand";

/** The public site: landing, features, pricing, contact, legal pages and the docs. */
export const PUBLIC_PAGES: { path: string; priority: number }[] = [
	{ path: "/", priority: 1 },
	{ path: "/features", priority: 0.9 },
	{ path: "/pricing", priority: 0.9 },
	{ path: "/docs", priority: 0.8 },
	{ path: "/contact", priority: 0.5 },
	{ path: "/privacy", priority: 0.3 },
	{ path: "/terms", priority: 0.3 },
];

/**
 * Every public page as an absolute URL on this server. A server with no
 * address (NEXT_PUBLIC_SITE_URL) lists nothing: a sitemap needs absolute URLs,
 * and any host but its own would send crawlers somewhere else.
 */
export function sitemapEntries(
	docPaths: string[],
	lastModified: Date,
	brand: Brand = BRAND
): MetadataRoute.Sitemap {
	const at = (path: string) => siteUrl(path, brand);
	if (!at("/")) return [];

	const pages: MetadataRoute.Sitemap = PUBLIC_PAGES.map(({ path, priority }) => ({
		url: at(path) as string,
		lastModified,
		changeFrequency: "weekly" as const,
		priority,
	}));

	const seen = new Set(pages.map((page) => page.url));
	const docs: MetadataRoute.Sitemap = docPaths
		.map((path) => at(path) as string)
		.filter((url) => !seen.has(url))
		.map((url) => ({ url, lastModified, changeFrequency: "weekly" as const, priority: 0.7 }));

	return [...pages, ...docs];
}
