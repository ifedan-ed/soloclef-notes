import type { QueryClient } from "@tanstack/react-query";
import { EnumConnectorName } from "@/contracts/enums/connector";
import type { SearchSourceConnector } from "@/contracts/types/connector.types";
import { connectorsApiService } from "@/lib/apis/connectors-api.service";
import { cacheKeys } from "@/lib/query-client/cache-keys";

/**
 * Which copy of a connector its edit view shows.
 *
 * A workspace's MCP servers reach "Your connectors" from Zero, which no
 * longer carries `config` (it holds the server's headers and tokens;
 * migration c0029), so such a row's config is the REST list's copy, or
 * `{}` until the list has the row: the edit view then opened on an empty
 * form, and an edit made elsewhere (a tool trusted from a chat, a revoke in
 * another tab, a teammate's change) never reached a view already open. So an
 * MCP server's view asks GET /search-source-connectors/{id} (secrets masked,
 * `mask_connector_config`) each time it opens and after each change made
 * from it, and shows a spinner until that answer is in. Other connections
 * are account-wide, never in Zero, and come with their config from the REST
 * list; their view reads the row as it is (the route answers 404 to anyone
 * but their owner).
 */
export function fetchesItsOwnConfig(connector: SearchSourceConnector): boolean {
	return connector.connector_type === EnumConnectorName.MCP_CONNECTOR && connector.id > 0;
}

/** The detail request an MCP server's edit view makes when it opens. */
export function connectorDetailQuery(connectorId: number) {
	return {
		queryKey: cacheKeys.connectors.byId(String(connectorId)),
		queryFn: () => connectorsApiService.getConnector({ id: connectorId }),
		// Asked again on every opening, never on a window focus: an answer
		// arriving mid-edit must not replace what is being typed.
		staleTime: 0,
		refetchOnMount: "always" as const,
		refetchOnWindowFocus: false,
	};
}

export interface EditableConnector {
	connector: SearchSourceConnector;
	/** Still waiting for the server's copy: no form yet, nothing to save. */
	pending: boolean;
}

/**
 * `row` (from "Your connectors" or a list) with the server's copy of what
 * Zero leaves out: its config and the vision switch. Fields only the
 * workspace list knows (`enabled_in_workspace`, `is_mine`) stay the row's.
 * A failed request leaves the row as it is rather than a spinner forever.
 */
export function editableConnector(
	row: SearchSourceConnector,
	fetched: SearchSourceConnector | undefined,
	failed = false
): EditableConnector {
	if (!fetchesItsOwnConfig(row)) return { connector: row, pending: false };
	if (!fetched || fetched.id !== row.id) return { connector: row, pending: !failed };
	return {
		connector: {
			...row,
			config: fetched.config,
			enable_vision_llm: fetched.enable_vision_llm,
		},
		pending: false,
	};
}

/**
 * After a change made from the view (Save, a trusted tool revoked): the
 * workspace list and this connector's own copy are asked again, so the view
 * and "Your connectors" show what the server now holds.
 */
export async function refreshConnectorAfterChange(
	client: QueryClient,
	workspaceId: string | number,
	connectorId: number
): Promise<void> {
	await Promise.all([
		client.invalidateQueries({ queryKey: cacheKeys.connectors.all(String(workspaceId)) }),
		client.invalidateQueries({ queryKey: cacheKeys.connectors.byId(String(connectorId)) }),
	]);
}
