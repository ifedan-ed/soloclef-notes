import type { SearchSourceConnector } from "@/contracts/types/connector.types";

/**
 * The one name for a connection's per-workspace on/off switch, used by the
 * switch itself and by every place that shows its state.
 */
export const ENABLED_IN_WORKSPACE_LABEL = "Enabled in this workspace";

/**
 * Whether a connection is on in the workspace it was listed for. Account-wide
 * connections are on everywhere unless their owner switched them off here
 * (custom/ACCOUNT_CONNECTIONS.md); MCP servers carry no flag and are always on
 * in their one workspace.
 */
export function isEnabledInWorkspace(connector: SearchSourceConnector): boolean {
	return connector.enabled_in_workspace !== false;
}
