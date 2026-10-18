import type { MetadataRoute } from "next";
import { BRAND } from "@/lib/brand";

/** The web app manifest: what a phone shows when the site is added to the home screen. */
export default function manifest(): MetadataRoute.Manifest {
	return {
		name: BRAND.name,
		short_name: BRAND.shortName,
		description: BRAND.description,
		start_url: "/dashboard",
		scope: "/",
		display: "standalone",
		background_color: BRAND.colors.backgroundDark,
		theme_color: BRAND.colors.ink,
		icons: [
			{ src: BRAND.logo.icon192, sizes: "192x192", type: "image/png", purpose: "any" },
			{ src: BRAND.logo.icon512, sizes: "512x512", type: "image/png", purpose: "any" },
			{ src: BRAND.logo.maskable512, sizes: "512x512", type: "image/png", purpose: "maskable" },
		],
	};
}
