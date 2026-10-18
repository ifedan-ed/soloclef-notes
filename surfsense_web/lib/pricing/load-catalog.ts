import "server-only";
import { SERVER_BACKEND_URL } from "@/lib/env-config";
import { coerceCatalog, DEFAULT_CATALOG, type PublicCatalog, parseBillingMode } from "./catalog";

export const PUBLIC_CATALOG_PATH = "/api/v1/billing/public/catalog";

/**
 * The catalog for public pages, read per request (cached 60 s) so an admin
 * price change shows without a rebuild. Falls back to the seeded defaults,
 * with the mode taken from the server's BILLING_MODE (default "shadow").
 */
export async function loadPublicCatalog(): Promise<PublicCatalog> {
	const fallback: PublicCatalog = {
		...DEFAULT_CATALOG,
		mode: parseBillingMode(process.env.BILLING_MODE, DEFAULT_CATALOG.mode),
	};
	if (!SERVER_BACKEND_URL) return fallback;
	try {
		const res = await fetch(`${SERVER_BACKEND_URL}${PUBLIC_CATALOG_PATH}`, {
			next: { revalidate: 60 },
			signal: AbortSignal.timeout(2500),
		});
		if (!res.ok) return fallback;
		return coerceCatalog(await res.json()) ?? fallback;
	} catch {
		return fallback;
	}
}
