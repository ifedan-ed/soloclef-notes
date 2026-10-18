import { EnumConnectorName } from "@/contracts/enums/connector";
import type { SearchSourceConnector } from "@/contracts/types/connector.types";

/**
 * Whose connection an account card shows. In a shared workspace a type's
 * list holds every member's connection that is on there, and they can all
 * carry the same name (every member's mail is "Email (IMAP)"), so each card
 * says whether it is the viewer's own. MCP servers belong to the workspace
 * and say nothing; nor does a row without `is_mine` (not from the REST list).
 */
export function accountOwnerLabel(
	connector: Pick<SearchSourceConnector, "connector_type" | "is_mine">
): string | null {
	if (connector.connector_type === EnumConnectorName.MCP_CONNECTOR) return null;
	if (connector.is_mine === true) return "Yours";
	if (connector.is_mine === false) return "Another member's";
	return null;
}

/**
 * Whether an account card shows "Last indexed" / "Never indexed". Not for a
 * live (MCP-backed) connection, and not for Email (IMAP): mail is read when
 * asked for and never indexed, so "Never indexed" would read as a fault.
 */
export function showsIndexedLine(
	connector: Pick<SearchSourceConnector, "connector_type" | "config">
): boolean {
	if (connector.config?.server_config) return false;
	return connector.connector_type !== EnumConnectorName.EMAIL_IMAP_CONNECTOR;
}
