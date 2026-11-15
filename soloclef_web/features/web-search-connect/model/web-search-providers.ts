import { settingsPath } from "@/lib/dashboard-paths";

/**
 * Exa and Tavily as native connectors: each is one API key of the person's
 * own, checked against the provider before it is saved and used by the
 * notebook research box and the chat's web search alike.
 */
export const WEB_SEARCH_PROVIDERS = ["exa", "tavily"] as const;
export type WebSearchProvider = (typeof WEB_SEARCH_PROVIDERS)[number];

export function isWebSearchProvider(value: unknown): value is WebSearchProvider {
	return typeof value === "string" && (WEB_SEARCH_PROVIDERS as readonly string[]).includes(value);
}

export interface WebSearchProviderInfo {
	title: string;
	/** The catalog's type for the card: its icon, its title and its search. */
	connectorType: string;
	/** Where the person makes a key at the provider. */
	keyPage: string;
}

export const WEB_SEARCH_PROVIDER_INFO: Record<WebSearchProvider, WebSearchProviderInfo> = {
	exa: {
		title: "Exa",
		connectorType: "EXA_WEB_SEARCH",
		keyPage: "https://dashboard.exa.ai/api-keys",
	},
	tavily: {
		title: "Tavily",
		connectorType: "TAVILY_WEB_SEARCH",
		keyPage: "https://app.tavily.com/home",
	},
};

/**
 * The connectors page with one provider's card opened for its key: where
 * "Not connected" in the research box leads. A query marker rather than
 * in-memory state, so it survives a reload and works from a new tab.
 */
export const CONNECT_PARAM = "connect";

export function webSearchConnectPath(provider: WebSearchProvider): string {
	return `${settingsPath("connectors")}?${CONNECT_PARAM}=${provider}`;
}

/** The provider the address asks to connect, or null. */
export function providerToConnect(params: URLSearchParams): WebSearchProvider | null {
	const asked = params.get(CONNECT_PARAM);
	return isWebSearchProvider(asked) ? asked : null;
}

/**
 * The research box's status is per notebook (["notebooks", id,
 * "research-sources"]); a key added or removed changes every notebook's.
 */
export function isResearchSourcesKey(queryKey: readonly unknown[]): boolean {
	return queryKey[0] === "notebooks" && queryKey[2] === "research-sources";
}
