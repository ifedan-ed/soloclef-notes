import type { ResearchSourcesStatus } from "@/features/notebooks/model/research";
import type { KeyedToolId } from "./composer-tools";

const NONE: ReadonlySet<KeyedToolId> = new Set();

/**
 * Which keyed tools this workspace has a key for, read from the research
 * box's status (the same keys answer both). Exa and Tavily are connected
 * unless the status lists them as unavailable; Browserbase when it is the
 * page reader. No status yet, or a failed one, offers none of them.
 */
export function connectedKeyedTools(
	status: ResearchSourcesStatus | undefined
): ReadonlySet<KeyedToolId> {
	if (!status) return NONE;
	const connected = new Set<KeyedToolId>();
	if (!status.unavailable.exa) connected.add("exa");
	if (!status.unavailable.tavily) connected.add("tavily");
	if (status.page_reader === "browserbase") connected.add("browserbase");
	return connected;
}
