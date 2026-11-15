/**
 * Coming back from Stalwart's sign-in page: the API's callback sends the
 * browser to the page the connect started on with `?jmap_mail=<outcome>`
 * (soloclef_api/internal/jmapconnect).
 */
export const JMAP_MAIL_PARAM = "jmap_mail";

export type JmapMailMessage = {
	tone: "success" | "error";
	/** Key in the `jmap_mail_connect` messages. */
	messageKey: string;
};

// One message per ending the callback names. An outcome this list does not
// know (a newer server) is "failed": it is not a success, and saying nothing
// would leave the person wondering whether the mailbox was added.
const ERROR_KEYS: Record<string, string> = {
	expired: "oauth_expired",
	no_mail: "oauth_no_mail",
	not_configured: "oauth_not_configured",
	failed: "oauth_failed",
};

/** What to tell the person; `null` when they cancelled (they know) or there is no outcome. */
export function jmapMailOutcome(outcome: string | null): JmapMailMessage | null {
	if (!outcome || outcome === "denied") return null;
	if (outcome === "connected") return { tone: "success", messageKey: "oauth_connected" };
	return { tone: "error", messageKey: ERROR_KEYS[outcome] ?? "oauth_failed" };
}

/** This page as a path to come back to, without a previous outcome on it. */
export function returnPathWithoutJmapOutcome(location: {
	pathname: string;
	search: string;
}): string {
	const params = new URLSearchParams(location.search);
	params.delete(JMAP_MAIL_PARAM);
	const query = params.toString();
	return `${location.pathname}${query ? `?${query}` : ""}`;
}

/**
 * The server address as typed, made into the one form the API takes: an
 * https origin. A bare host ("mail.example.com") gets https:// in front; a
 * path, query or anything but https is refused here, so the person is told at
 * the field rather than by a round trip. The server checks it again: this is
 * only for the form.
 */
export function normalizedServerUrl(typed: string): string | null {
	const trimmed = typed.trim();
	if (!trimmed) return null;
	const withScheme = /^[a-z][a-z0-9+.-]*:\/\//i.test(trimmed) ? trimmed : `https://${trimmed}`;
	let parsed: URL;
	try {
		parsed = new URL(withScheme);
	} catch {
		return null;
	}
	if (parsed.protocol !== "https:" || parsed.username || parsed.password) return null;
	if (parsed.pathname !== "/" || parsed.search || parsed.hash) return null;
	if (!parsed.hostname.includes(".")) return null;
	return parsed.origin;
}

/** "mail.example.com" for a row, from the stored https origin. */
export function serverHost(serverUrl: string): string {
	try {
		return new URL(serverUrl).host;
	} catch {
		return serverUrl;
	}
}
