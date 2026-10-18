import "server-only";
import { SERVER_BACKEND_URL } from "@/lib/env-config";
import { type PublicLinkSummary, summarizePublicLink } from "./public-link-metadata";

/** How long the page waits for the link's title before falling back to the site's name. */
const SUMMARY_TIMEOUT_MS = 2500;

/**
 * What a public link shares, read on the server for the page's title and
 * link preview. Never cached: a revoked or expired link stops naming its
 * item at once. Null when the link is gone or the server does not answer
 * in time; the page itself then says why.
 */
export async function loadPublicLinkSummary(token: string): Promise<PublicLinkSummary | null> {
	if (!SERVER_BACKEND_URL) return null;
	try {
		const res = await fetch(
			`${SERVER_BACKEND_URL.replace(/\/$/, "")}/api/v1/public/${encodeURIComponent(token)}`,
			{ cache: "no-store", signal: AbortSignal.timeout(SUMMARY_TIMEOUT_MS) }
		);
		if (!res.ok) return null;
		return summarizePublicLink(await res.json());
	} catch {
		return null;
	}
}
