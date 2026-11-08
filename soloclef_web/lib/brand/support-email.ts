import { buildBackendUrl } from "@/lib/env-config";
import { BRAND } from "./index";

/**
 * Where "Report Issue" mails go. An admin sets it on Billing -> Admin ->
 * Settings (backend `support_email`, else SUPPORT_EMAIL, read from
 * `GET /api/v1/support-contact`); until it has loaded, or if it cannot, the
 * build's NEXT_PUBLIC_SUPPORT_EMAIL (`BRAND.contactEmail`) is used, which is
 * empty on a server that set none.
 */
export const SUPPORT_CONTACT_PATH = "/api/v1/support-contact";

const PLAIN_EMAIL = /^[^@\s<>"',;:?&]+@[^@\s<>"',;:?&]+\.[^@\s<>"',;:?&]+$/;

/** A single plain address from the endpoint's body, or null. */
export function parseSupportEmail(body: unknown): string | null {
	if (!body || typeof body !== "object") return null;
	const email = (body as { email?: unknown }).email;
	if (typeof email !== "string") return null;
	const trimmed = email.trim();
	return trimmed.length <= 254 && PLAIN_EMAIL.test(trimmed) ? trimmed : null;
}

let loaded: string | null = null;
let pending: Promise<string> | null = null;

/** The address known now: the loaded one, else the brand default. */
export function currentSupportEmail(fallback: string = BRAND.contactEmail): string {
	return loaded ?? fallback;
}

/** Fetch the address once per page load; never rejects. */
export function loadSupportEmail(
	fetcher: typeof fetch = fetch,
	fallback: string = BRAND.contactEmail
): Promise<string> {
	if (loaded) return Promise.resolve(loaded);
	if (!pending) {
		pending = fetcher(buildBackendUrl(SUPPORT_CONTACT_PATH), {
			signal: AbortSignal.timeout(2500),
		})
			.then(async (res) => (res.ok ? parseSupportEmail(await res.json()) : null))
			.catch(() => null)
			.then((email) => {
				if (email) loaded = email;
				else pending = null;
				return email ?? fallback;
			});
	}
	return pending;
}

/** For tests: forget what was loaded. */
export function resetSupportEmailForTests(): void {
	loaded = null;
	pending = null;
}
