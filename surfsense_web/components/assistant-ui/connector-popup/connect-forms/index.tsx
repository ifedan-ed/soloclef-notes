import dynamic from "next/dynamic";
import type { FC } from "react";

export interface ConnectFormProps {
	onSubmit: (data: {
		name: string;
		connector_type: string;
		config: Record<string, unknown>;
		is_indexable: boolean;
		is_active: boolean;
		last_indexed_at: null;
		periodic_indexing_enabled: boolean;
		indexing_frequency_minutes: number | null;
		next_scheduled_at: null;
		startDate?: Date;
		endDate?: Date;
		periodicEnabled?: boolean;
		frequencyMinutes?: string;
	}) => Promise<void>;
	onBack: () => void;
	isSubmitting: boolean;
	onFormSubmit?: () => void;
}

export type ConnectFormComponent = FC<ConnectFormProps>;

const formMap: Record<string, () => Promise<{ default: FC<ConnectFormProps> }>> = {
	MCP_CONNECTOR: () =>
		import("./components/mcp-connect-form").then((m) => ({ default: m.MCPConnectForm })),
};

const componentCache = new Map<string, ConnectFormComponent>();

/**
 * Factory function to get the appropriate connect form component for a connector type
 */
export function getConnectFormComponent(connectorType: string): ConnectFormComponent | null {
	const loader = formMap[connectorType];
	if (!loader) return null;

	if (!componentCache.has(connectorType)) {
		componentCache.set(connectorType, dynamic(loader, { ssr: false }));
	}

	return componentCache.get(connectorType)!;
}
