import type { MetadataRoute } from "next";
import { sitemapEntries } from "@/lib/seo/sitemap";
import { source as docsSource } from "@/lib/source";

export default function sitemap(): MetadataRoute.Sitemap {
	const lastModified = new Date();
	lastModified.setMinutes(0, 0, 0);
	return sitemapEntries(
		docsSource.getPages().map((page) => page.url),
		lastModified
	);
}
