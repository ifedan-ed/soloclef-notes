import "server-only";
import { SERVER_BACKEND_URL } from "@/lib/env-config";
import { BRAND } from "./index";
import { parseSupportEmail, SUPPORT_CONTACT_PATH } from "./support-email";

/**
 * The admin-set support address for server-rendered pages (the contact page),
 * read per request and cached for a minute, so an edit shows without a
 * rebuild. Falls back to the build's NEXT_PUBLIC_SUPPORT_EMAIL, which is empty
 * on a server that published none (the contact page then says so).
 */
export async function loadSupportEmailOnServer(): Promise<string> {
	if (!SERVER_BACKEND_URL) return BRAND.contactEmail;
	try {
		const res = await fetch(`${SERVER_BACKEND_URL}${SUPPORT_CONTACT_PATH}`, {
			next: { revalidate: 60 },
			signal: AbortSignal.timeout(2500),
		});
		if (!res.ok) return BRAND.contactEmail;
		return parseSupportEmail(await res.json()) ?? BRAND.contactEmail;
	} catch {
		return BRAND.contactEmail;
	}
}
