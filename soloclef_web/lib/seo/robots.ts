import type { MetadataRoute } from "next";
import { BRAND, type Brand, siteUrl } from "@/lib/brand";

/** What crawlers may read; the sitemap line only when the server has an address. */
export function robotsRules(brand: Brand = BRAND): MetadataRoute.Robots {
	return {
		rules: [
			{
				userAgent: "*",
				allow: "/",
				disallow: [
					"/dashboard/",
					"/desktop/",
					"/auth/",
					"/api/",
					"/invite/",
					"/public/",
					"/verify-token/",
					"/login",
					"/register",
					"/forgot-password",
					"/reset-password",
				],
			},
		],
		sitemap: siteUrl("/sitemap.xml", brand),
	};
}
