"use client";

import dynamic from "next/dynamic";
import type { FC } from "react";
import type { SearchSourceConnector } from "@/contracts/types/connector.types";

export interface ConnectorConfigProps {
	connector: SearchSourceConnector;
	onConfigChange?: (config: Record<string, unknown>) => void;
	onNameChange?: (name: string) => void;
	workspaceId?: string;
}

export type ConnectorConfigComponent = FC<ConnectorConfigProps>;

const configMap: Record<string, () => Promise<{ default: FC<ConnectorConfigProps> }>> = {
	GOOGLE_DRIVE_CONNECTOR: () =>
		import("./components/google-drive-config").then((m) => ({ default: m.GoogleDriveConfig })),
	WEBCRAWLER_CONNECTOR: () =>
		import("./components/webcrawler-config").then((m) => ({ default: m.WebcrawlerConfig })),
	MCP_CONNECTOR: () => import("./components/mcp-config").then((m) => ({ default: m.MCPConfig })),
	NEXTCLOUD_CONNECTOR: () =>
		import("@/features/nextcloud-files/ui/nextcloud-connector-config").then((m) => ({
			default: m.NextcloudConnectorConfig,
		})),
};

const componentCache = new Map<string, ConnectorConfigComponent>();

/**
 * Factory function to get the appropriate config component for a connector type
 */
export function getConnectorConfigComponent(
	connectorType: string
): ConnectorConfigComponent | null {
	const loader = configMap[connectorType];
	if (!loader) return null;

	if (!componentCache.has(connectorType)) {
		componentCache.set(connectorType, dynamic(loader, { ssr: false }));
	}

	return componentCache.get(connectorType)!;
}
