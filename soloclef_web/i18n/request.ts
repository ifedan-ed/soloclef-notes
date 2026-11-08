import { getRequestConfig } from "next-intl/server";
import { withBrand } from "@/lib/brand/messages";
import { routing } from "./routing";

/**
 * Configuration for internationalization request handling
 * This function is called for each request to determine the locale and load translations
 */
export default getRequestConfig(async ({ requestLocale }) => {
	// This typically corresponds to the `[locale]` segment
	let locale = await requestLocale;

	// Ensure that the incoming `locale` is valid
	if (!locale || !routing.locales.includes(locale as (typeof routing.locales)[number])) {
		locale = routing.defaultLocale;
	}

	return {
		locale,
		messages: withBrand((await import(`../messages/${locale}.json`)).default),
		// Set a default timeZone to prevent hydration mismatches between server and client
		timeZone: "UTC",
	};
});
