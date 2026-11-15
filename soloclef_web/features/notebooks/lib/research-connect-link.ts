import {
	isWebSearchProvider,
	type WebSearchProvider,
	webSearchConnectPath,
} from "@/features/web-search-connect/model/web-search-providers";
import type { ResearchBoxSource, SourceUnavailableReason } from "../model/research-sources";

/**
 * The research box's way to a missing key. A web search source that is
 * unavailable because no key is connected is not a dead row: picking it
 * opens the connectors page on that provider's key field. A source this
 * server does not search ("not_available") has nothing to connect and stays
 * greyed out.
 */
export function connectableSource(
	id: ResearchBoxSource,
	reason: SourceUnavailableReason | undefined
): WebSearchProvider | null {
	return reason === "not_connected" && isWebSearchProvider(id) ? id : null;
}

/** Where picking that row goes. */
export function connectLinkFor(provider: WebSearchProvider): string {
	return webSearchConnectPath(provider);
}
