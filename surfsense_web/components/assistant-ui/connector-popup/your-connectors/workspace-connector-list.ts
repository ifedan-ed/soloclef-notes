import type { SearchSourceConnector } from "@/contracts/types/connector.types";

/**
 * Every connection "Your connectors" lists for one workspace, each once: the
 * workspace's own connectors (its MCP servers) and every account-wide
 * connection usable in it (Email, Nextcloud, Google Drive, web pages).
 *
 * The two sources hold different halves. Zero (`live`) syncs only rows whose
 * workspace_id is this workspace, so it never has an account-wide connection
 * (workspace_id NULL, custom/ACCOUNT_CONNECTIONS.md). The REST list (`listed`,
 * GET /search-source-connectors?workspace_id=) has both, plus what only the
 * server knows: whether each connection is on here (`enabled_in_workspace`)
 * and whether the caller owns it (`is_mine`).
 *
 * So a live row replaces its listed copy and keeps those two fields, and its
 * config and vision switch: Zero never syncs them (config holds the
 * connector's keys; migration c0029), so a live row's config is the REST
 * copy's, with secrets masked, or empty until the REST list has the row (an
 * MCP server's edit view asks the server for its own, editable-connector.ts).
 * While
 * Zero holds at least one of the workspace's rows, a listed workspace
 * connector missing from them was removed and is dropped at once. When Zero
 * holds none (still loading, its last row was just removed, or Zero refused
 * the query), the REST list is used as it is: the panel refetches that list
 * whenever Zero's set of rows changes (liveRowsChanged), so the removal of
 * the last MCP server reaches it a moment later.
 */
export function workspaceConnectorList(
	live: SearchSourceConnector[],
	listed: SearchSourceConnector[] | undefined
): SearchSourceConnector[] {
	const listedById = new Map((listed ?? []).map((c) => [c.id, c]));
	const liveIds = new Set(live.map((c) => c.id));
	const merged = live.map((c) => {
		const copy = listedById.get(c.id);
		return copy
			? {
					...c,
					config: copy.config,
					enable_vision_llm: copy.enable_vision_llm,
					enabled_in_workspace: copy.enabled_in_workspace,
					is_mine: copy.is_mine,
				}
			: c;
	});
	for (const c of listed ?? []) {
		if (liveIds.has(c.id)) continue;
		if (live.length > 0 && typeof c.workspace_id === "number") continue;
		merged.push(c);
	}
	return merged;
}

/** Zero's rows for the workspace as a comparable key: their ids, sorted. */
export function liveRowsKey(live: readonly SearchSourceConnector[]): string {
	return live
		.map((c) => c.id)
		.sort((a, b) => a - b)
		.join(",");
}

/**
 * Whether Zero's set of workspace rows changed since the last complete
 * result (`previousKey`, null before the first one). A change means an MCP
 * server was added or removed, maybe in another tab or by a teammate, and
 * the REST list (which the MCP servers' own list view also reads, and which
 * workspaceConnectorList falls back to when Zero holds none) is out of date.
 */
export function liveRowsChanged(
	previousKey: string | null,
	live: readonly SearchSourceConnector[]
): boolean {
	return previousKey !== null && previousKey !== liveRowsKey(live);
}
