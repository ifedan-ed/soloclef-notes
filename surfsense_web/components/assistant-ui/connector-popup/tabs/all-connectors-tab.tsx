"use client";

import type { FC, ReactNode } from "react";
import { useIsSelfHosted } from "@/components/providers/runtime-config";
import { EnumConnectorName } from "@/contracts/enums/connector";
import type { SearchSourceConnector } from "@/contracts/types/connector.types";
import { EmailImapConnectorCard } from "@/features/email-imap-connect/ui/email-imap-connector-card";
import { GoogleDriveConnectorCard } from "@/features/google-drive-connect/ui/google-drive-connector-card";
import { NextcloudConnectorCard } from "@/features/nextcloud-connect/ui/nextcloud-connector-card";
import { usePlatform } from "@/hooks/use-platform";
import { ConnectorCard } from "../components/connector-card";
import {
	CRAWLERS,
	DEPRECATED_CONNECTOR_TYPES,
	NATIVE_CONNECTORS,
	OAUTH_CONNECTORS,
	OTHER_CONNECTORS,
} from "../constants/connector-constants";
import { getDocumentCountForConnector } from "../utils/connector-document-mapping";

type OtherConnector = (typeof OTHER_CONNECTORS)[number];
type CrawlerConnector = (typeof CRAWLERS)[number];
type DeploymentFilterableConnector = {
	readonly id: string;
	readonly selfHostedOnly?: boolean;
	readonly desktopOnly?: boolean;
};

/**
 * Extract the display name from a full connector name.
 * Full names are in format "Base Name - identifier" (e.g., "Google Drive - john@example.com").
 * Returns just the identifier (e.g : john@example.com).
 */
export function getConnectorDisplayName(fullName: string): string {
	const separatorIndex = fullName.indexOf(" - ");
	if (separatorIndex !== -1) {
		return fullName.substring(separatorIndex + 3);
	}
	return fullName;
}

interface AllConnectorsTabProps {
	searchQuery: string;
	workspaceId: string;
	connectedTypes: Set<string>;
	connectingId: string | null;
	allConnectors: SearchSourceConnector[] | undefined;
	documentTypeCounts?: Record<string, number>;
	indexingConnectorIds?: Set<number>;
	onConnectNonOAuth?: (connectorType: string) => void;
	onCreateWebcrawler?: () => void;
	onManage?: (connector: SearchSourceConnector) => void;
}

export const AllConnectorsTab: FC<AllConnectorsTabProps> = ({
	searchQuery,
	workspaceId,
	connectedTypes,
	connectingId,
	allConnectors,
	documentTypeCounts,
	indexingConnectorIds,
	onConnectNonOAuth,
	onCreateWebcrawler,
	onManage,
}) => {
	const selfHosted = useIsSelfHosted();
	const { isDesktop } = usePlatform();

	const matchesSearch = (title: string, description: string) =>
		title.toLowerCase().includes(searchQuery.toLowerCase()) ||
		description.toLowerCase().includes(searchQuery.toLowerCase());

	const passesDeploymentFilter = (c: DeploymentFilterableConnector) =>
		(!c.selfHostedOnly || selfHosted) && (!c.desktopOnly || isDesktop);

	// Filter connectors based on search and deployment mode
	const filteredOAuth = OAUTH_CONNECTORS.filter(
		(c) => matchesSearch(c.title, c.description) && passesDeploymentFilter(c)
	);

	const filteredCrawlers = CRAWLERS.filter(
		(c) => matchesSearch(c.title, c.description) && passesDeploymentFilter(c)
	);

	const filteredOther = OTHER_CONNECTORS.filter(
		(c) => matchesSearch(c.title, c.description) && passesDeploymentFilter(c)
	);

	// One flat grid, no separate "Deprecated" section. A deprecated connector is
	// shown only if the user already connected it (before deprecation), so it
	// stays manageable/disconnectable; never-connected deprecated connectors are
	// hidden entirely. See DEPRECATED_CONNECTOR_TYPES.
	const isVisible = <T extends { connectorType?: string }>(c: T) =>
		!c.connectorType ||
		!DEPRECATED_CONNECTOR_TYPES.has(c.connectorType) ||
		connectedTypes.has(c.connectorType);
	const available = {
		oauth: filteredOAuth.filter(isVisible),
		other: filteredOther.filter(isVisible),
		crawlers: filteredCrawlers.filter(isVisible),
	};

	const renderOtherCard = (connector: OtherConnector) => {
		const isConnected = connectedTypes.has(connector.connectorType);
		const isConnecting = connectingId === connector.id;

		const actualConnector =
			isConnected && allConnectors
				? allConnectors.find(
						(c: SearchSourceConnector) => c.connector_type === connector.connectorType
					)
				: undefined;

		const documentCount = getDocumentCountForConnector(connector.connectorType, documentTypeCounts);
		const isIndexing = actualConnector && indexingConnectorIds?.has(actualConnector.id);

		const isMCP = connector.connectorType === EnumConnectorName.MCP_CONNECTOR;
		const mcpConnectorCount =
			isMCP && allConnectors
				? allConnectors.filter(
						(c: SearchSourceConnector) => c.connector_type === EnumConnectorName.MCP_CONNECTOR
					).length
				: undefined;

		const handleConnect = onConnectNonOAuth
			? () => onConnectNonOAuth(connector.connectorType)
			: () => {};

		return (
			<ConnectorCard
				key={connector.id}
				id={connector.id}
				title={connector.title}
				description={connector.description}
				connectorType={connector.connectorType}
				isConnected={isConnected}
				isConnecting={isConnecting}
				documentCount={documentCount}
				connectorCount={mcpConnectorCount}
				isIndexing={isIndexing}
				onConnect={handleConnect}
				onManage={actualConnector && onManage ? () => onManage(actualConnector) : undefined}
			/>
		);
	};

	const renderCrawlerCard = (crawler: CrawlerConnector) => {
		const isWebcrawler = crawler.id === "webcrawler-connector";
		const isConnected = crawler.connectorType ? connectedTypes.has(crawler.connectorType) : false;
		const isConnecting = connectingId === crawler.id;

		const actualConnector =
			isConnected && crawler.connectorType && allConnectors
				? allConnectors.find(
						(c: SearchSourceConnector) => c.connector_type === crawler.connectorType
					)
				: undefined;

		const documentCount = crawler.connectorType
			? getDocumentCountForConnector(crawler.connectorType, documentTypeCounts)
			: undefined;
		const isIndexing = actualConnector && indexingConnectorIds?.has(actualConnector.id);

		const handleConnect =
			isWebcrawler && onCreateWebcrawler
				? onCreateWebcrawler
				: crawler.connectorType && onConnectNonOAuth
					? () => {
							if (crawler.connectorType) {
								onConnectNonOAuth(crawler.connectorType);
							}
						}
					: () => {};

		return (
			<ConnectorCard
				key={crawler.id}
				id={crawler.id}
				title={crawler.title}
				description={crawler.description}
				connectorType={crawler.connectorType || undefined}
				isConnected={isConnected}
				isConnecting={isConnecting}
				documentCount={documentCount}
				isIndexing={isIndexing}
				onConnect={handleConnect}
				onManage={actualConnector && onManage ? () => onManage(actualConnector) : undefined}
			/>
		);
	};

	const hasAnyResults =
		NATIVE_CONNECTORS.some((c) => matchesSearch(c.title, c.description)) ||
		available.oauth.length > 0 ||
		available.other.length > 0 ||
		available.crawlers.length > 0;

	if (!hasAnyResults && searchQuery) {
		return (
			<div className="flex flex-col items-center justify-center py-20 text-center">
				<p className="text-sm text-muted-foreground">No connectors found</p>
			</div>
		);
	}

	// Google Drive and Nextcloud connect through their own features' cards,
	// Drive first and Nextcloud right after it.
	const manageById = (connectorId: number) => {
		const connector = allConnectors?.find((c) => c.id === connectorId);
		if (connector && onManage) onManage(connector);
	};
	const nativeCards: ReactNode[] = [];
	if (available.oauth.some((c) => c.connectorType === EnumConnectorName.GOOGLE_DRIVE_CONNECTOR)) {
		nativeCards.push(
			<GoogleDriveConnectorCard
				key="google-drive-connector"
				workspaceId={Number(workspaceId)}
				onManage={manageById}
			/>
		);
	}
	if (
		NATIVE_CONNECTORS.some(
			(c) =>
				c.connectorType === EnumConnectorName.NEXTCLOUD_CONNECTOR &&
				matchesSearch(c.title, c.description)
		)
	) {
		nativeCards.push(
			<NextcloudConnectorCard
				key="nextcloud-connector"
				workspaceId={Number(workspaceId)}
				onManage={manageById}
			/>
		);
	}
	if (
		NATIVE_CONNECTORS.some(
			(c) =>
				c.connectorType === EnumConnectorName.EMAIL_IMAP_CONNECTOR &&
				matchesSearch(c.title, c.description)
		)
	) {
		nativeCards.push(
			<EmailImapConnectorCard key="email-imap-connector" workspaceId={Number(workspaceId)} />
		);
	}

	return (
		<div className="space-y-8">
			<div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
				{nativeCards}
				{available.crawlers.map(renderCrawlerCard)}
				{available.other.map(renderOtherCard)}
			</div>
		</div>
	);
};
