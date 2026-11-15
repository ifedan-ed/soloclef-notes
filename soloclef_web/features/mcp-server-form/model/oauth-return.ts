/**
 * The end of an MCP server's OAuth sign-in. The provider sends the person
 * back through the API, which lands them on the page the sign-in started
 * from with `?mcp_oauth=<outcome>&mcp_connector=<id>`. These are the two
 * markers, what each outcome says, and the address the sign-in asks to be
 * sent back to.
 */

export const MCP_OAUTH_PARAM = "mcp_oauth";
export const MCP_CONNECTOR_PARAM = "mcp_connector";

export type OAuthOutcome = "connected" | "denied" | "failed" | "expired";

export interface OAuthReturn {
	outcome: OAuthOutcome;
	/** The server that was signed in to, when the address names one. */
	connectorId: number | null;
	tone: "success" | "error";
	message: string;
}

const MESSAGES: Record<OAuthOutcome, { tone: "success" | "error"; message: string }> = {
	connected: { tone: "success", message: "Signed in. The server's tools are ready in your chats." },
	denied: { tone: "error", message: "The sign-in was cancelled, so the server is not signed in." },
	failed: {
		tone: "error",
		message: "The sign-in did not finish. Try again from the server's settings.",
	},
	expired: {
		tone: "error",
		message: "The sign-in took too long or started in another browser. Start it again.",
	},
};

/**
 * The outcome the address carries, or null when it carries none. An
 * outcome this app does not know is said as a failure rather than dropped:
 * the person came back from a sign-in and should hear how it went.
 */
export function readOAuthReturn(params: URLSearchParams): OAuthReturn | null {
	const said = params.get(MCP_OAUTH_PARAM);
	if (!said) return null;
	const outcome: OAuthOutcome = said in MESSAGES ? (said as OAuthOutcome) : "failed";
	const rawId = params.get(MCP_CONNECTOR_PARAM);
	const id = rawId && /^\d+$/.test(rawId) ? Number(rawId) : null;
	return { outcome, connectorId: id && Number.isSafeInteger(id) ? id : null, ...MESSAGES[outcome] };
}

/** The query without the two markers, so a reload does not say it again. */
export function withoutOAuthMarkers(params: URLSearchParams): string {
	const next = new URLSearchParams(params.toString());
	next.delete(MCP_OAUTH_PARAM);
	next.delete(MCP_CONNECTOR_PARAM);
	const query = next.toString();
	return query ? `?${query}` : "";
}

/**
 * Where the provider sends the person back: this page, as a path of this
 * site, without markers left from an earlier sign-in. The API accepts only
 * a same-site path, so nothing but the path and query is sent.
 */
export function oauthReturnPath(pathname: string, search: string): string {
	const path = pathname.startsWith("/") && !pathname.startsWith("//") ? pathname : "/";
	return `${path}${withoutOAuthMarkers(new URLSearchParams(search))}`;
}
