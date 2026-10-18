import { EnumConnectorName } from "@/contracts/enums/connector";
import type { SearchSourceConnector } from "@/contracts/types/connector.types";
import { isEnabledInWorkspace } from "../workspace-enablement/enabled-in-workspace";

/**
 * Connectors kept in the code but hidden from the catalog unless the user
 * already has one, so an existing connection stays manageable.
 * New web-page imports go through the file Import menu instead.
 */
export const DEPRECATED_CONNECTOR_TYPES = new Set<string>([EnumConnectorName.WEBCRAWLER_CONNECTOR]);

// Native connectors with their own connect flow (Nextcloud's Login Flow,
// Email's own add-account form): rendered by their feature's card, listed
// here for titles and search.
export const NATIVE_CONNECTORS = [
	{
		id: "nextcloud-connector",
		title: "Nextcloud",
		description: "Connect your Nextcloud files",
		connectorType: EnumConnectorName.NEXTCLOUD_CONNECTOR,
	},
	{
		id: "email-imap-connector",
		title: "Email (IMAP)",
		description: "Search and read your own mailboxes, read-only",
		connectorType: EnumConnectorName.EMAIL_IMAP_CONNECTOR,
	},
] as const;

// OAuth Connectors (Quick Connect)
export const OAUTH_CONNECTORS = [
	{
		id: "google-drive-connector",
		title: "Google Drive",
		description: "Search your Drive files",
		connectorType: EnumConnectorName.GOOGLE_DRIVE_CONNECTOR,
		authEndpoint: "/api/v1/auth/google/drive/connector/add/",
		selfHostedOnly: true,
	},
] as const;

// Content Sources (tools that extract and import content from external sources)
export const CRAWLERS = [
	{
		id: "webcrawler-connector",
		title: "Web Pages",
		description: "Index and periodically sync web content",
		connectorType: EnumConnectorName.WEBCRAWLER_CONNECTOR,
	},
] as const;

// Non-OAuth Connectors (connect through a form in the panel)
export const OTHER_CONNECTORS = [
	{
		id: "mcp-connector",
		title: "MCPs",
		description: "Connect to MCP servers for AI tools",
		connectorType: EnumConnectorName.MCP_CONNECTOR,
	},
] as const;

export const CONNECTOR_DISPLAY_DEFINITIONS = [
	...OAUTH_CONNECTORS,
	...NATIVE_CONNECTORS,
	...CRAWLERS,
	...OTHER_CONNECTORS,
] as const;

export function getConnectorTitle(connectorType: string): string {
	return (
		CONNECTOR_DISPLAY_DEFINITIONS.find((connector) => connector.connectorType === connectorType)
			?.title ?? connectorType
	);
}

/**
 * The accounts list to open for a connector type with several connections,
 * or null for MCP servers (they have their own list). Every type gets one,
 * not only the OAuth ones: in a shared workspace each member's Email (IMAP)
 * or Nextcloud is its own connection.
 */
export function accountsListFor(
	connectorType: string,
	connectorTitle?: string
): { connectorType: string; connectorTitle: string } | null {
	if (connectorType === EnumConnectorName.MCP_CONNECTOR) return null;
	const oauthConnector = OAUTH_CONNECTORS.find((c) => c.connectorType === connectorType);
	return {
		connectorType,
		connectorTitle: oauthConnector?.title ?? connectorTitle ?? getConnectorTitle(connectorType),
	};
}

/** One row per connected connector type (multiple accounts collapsed into one). */
export interface ConnectorTypeRow {
	type: string;
	title: string;
	connectors: SearchSourceConnector[];
	accountCount: number;
	/** How many of `connectors` are switched on in the listed workspace. */
	enabledCount: number;
}

/**
 * An Email (IMAP) connection keeps its row after its last mailbox is removed
 * (the mailboxes are its config's `accounts`). Such a row connects nothing,
 * so "Your connectors" and the composer's menu leave it out.
 */
export function connectsNothing(connector: SearchSourceConnector): boolean {
	const accounts = connector.config?.accounts;
	return (
		connector.connector_type === EnumConnectorName.EMAIL_IMAP_CONNECTOR &&
		Array.isArray(accounts) &&
		accounts.length === 0
	);
}

/**
 * Collapse a flat connector list into one row per `connector_type`, titled for
 * display (MCP collapses to "MCP Servers") and sorted alphabetically. Shared by
 * the connectors panel's "Your connectors" rail and the composer add-menu so the
 * two lists never drift; callers layer their own extras (e.g. health) on top.
 */
export function groupConnectorsByType(connectors: SearchSourceConnector[]): ConnectorTypeRow[] {
	const byType = new Map<string, SearchSourceConnector[]>();
	for (const c of connectors) {
		if (connectsNothing(c)) continue;
		const arr = byType.get(c.connector_type) ?? [];
		arr.push(c);
		byType.set(c.connector_type, arr);
	}
	return [...byType.entries()]
		.map(([type, list]) => ({
			type,
			title: type === EnumConnectorName.MCP_CONNECTOR ? "MCP Servers" : getConnectorTitle(type),
			connectors: list,
			accountCount: list.length,
			enabledCount: list.filter(isEnabledInWorkspace).length,
		}))
		.sort((a, b) => a.title.localeCompare(b.title));
}

// Re-export IndexingConfigState from schemas for backward compatibility
export type { IndexingConfigState } from "./connector-popup.schemas";
