import {
	CRAWLERS,
	OAUTH_CONNECTORS,
	OTHER_CONNECTORS,
} from "@/components/assistant-ui/connector-popup/constants/connector-constants";
import { EnumConnectorName } from "@/contracts/enums/connector";
import type { SearchSourceConnector } from "@/contracts/types/connector.types";

// =============================================================================
// Connector Telemetry Types & Registry
// =============================================================================

export type ConnectorTelemetryGroup = "oauth" | "crawler" | "other" | "unknown";

export interface ConnectorTelemetryMeta {
	connector_type: string;
	connector_title: string;
	connector_group: ConnectorTelemetryGroup;
	is_oauth: boolean;
}

/**
 * Single source of truth for "what does this connector_type look like in
 * analytics?". Any connector added to the lists above is automatically
 * picked up here, so adding a new integration does NOT require touching
 * `lib/posthog/events.ts` or per-connector tracking code.
 */
let connectorTelemetryRegistry: ReadonlyMap<string, ConnectorTelemetryMeta> | undefined;

function getConnectorTelemetryRegistry(): ReadonlyMap<string, ConnectorTelemetryMeta> {
	if (connectorTelemetryRegistry) return connectorTelemetryRegistry;

	const map = new Map<string, ConnectorTelemetryMeta>();

	for (const c of OAUTH_CONNECTORS) {
		map.set(c.connectorType, {
			connector_type: c.connectorType,
			connector_title: c.title,
			connector_group: "oauth",
			is_oauth: true,
		});
	}
	for (const c of CRAWLERS) {
		map.set(c.connectorType, {
			connector_type: c.connectorType,
			connector_title: c.title,
			connector_group: "crawler",
			is_oauth: false,
		});
	}
	for (const c of OTHER_CONNECTORS) {
		map.set(c.connectorType, {
			connector_type: c.connectorType,
			connector_title: c.title,
			connector_group: "other",
			is_oauth: false,
		});
	}

	connectorTelemetryRegistry = map;
	return connectorTelemetryRegistry;
}

/**
 * Returns telemetry metadata for a connector_type, or a minimal "unknown"
 * record so tracking never no-ops for connectors that exist in the backend
 * but were forgotten in the UI registry.
 */
export function getConnectorTelemetryMeta(connectorType: string): ConnectorTelemetryMeta {
	const hit = getConnectorTelemetryRegistry().get(connectorType);
	if (hit) return hit;

	return {
		connector_type: connectorType,
		connector_title: connectorType,
		connector_group: "unknown",
		is_oauth: false,
	};
}

// =============================================================================
// Reauth Endpoint Resolution
// =============================================================================

/**
 * OAuth reauth endpoints, keyed by connector type.
 */
const REAUTH_ENDPOINTS: Partial<Record<string, string>> = {
	[EnumConnectorName.GOOGLE_DRIVE_CONNECTOR]: "/api/v1/auth/google/drive/connector/reauth",
};

/**
 * Resolve the reauth endpoint for a connector, or undefined when it has no
 * OAuth grant to refresh.
 */
export function getReauthEndpoint(connector: SearchSourceConnector): string | undefined {
	return REAUTH_ENDPOINTS[connector.connector_type];
}
