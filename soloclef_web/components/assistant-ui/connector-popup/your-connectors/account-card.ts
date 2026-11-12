import { EnumConnectorName } from "@/contracts/enums/connector";
import type { SearchSourceConnector } from "@/contracts/types/connector.types";

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
