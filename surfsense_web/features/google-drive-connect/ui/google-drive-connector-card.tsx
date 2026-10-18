"use client";

import { useTranslations } from "next-intl";
import { ConnectorCard } from "@/components/assistant-ui/connector-popup/components/connector-card";
import { EnumConnectorName } from "@/contracts/enums/connector";
import { useGoogleDriveConnect } from "../hooks/use-google-drive-connect";
import { useGoogleDriveConnection } from "../hooks/use-google-drive-connection";
import { useGoogleDriveReturn } from "../hooks/use-google-drive-return";

/**
 * Google Drive in the connectors catalog: Connect goes to Google's consent
 * page; once connected, Manage opens the connection.
 */
export function GoogleDriveConnectorCard({
	workspaceId,
	onManage,
}: {
	workspaceId: number;
	onManage: (connectorId: number) => void;
}) {
	const t = useTranslations("google_drive_connect");
	const { connection, isConnected, refresh } = useGoogleDriveConnection(workspaceId);
	useGoogleDriveReturn(refresh);
	const { start, starting } = useGoogleDriveConnect(workspaceId);
	const configured = connection?.configured !== false;

	return (
		<div className="flex flex-col gap-2" data-testid="google-drive-connector-card">
			<ConnectorCard
				id="google-drive-connector"
				title={t("title")}
				description={configured ? t("card_description") : t("not_configured")}
				connectorType={EnumConnectorName.GOOGLE_DRIVE_CONNECTOR}
				isConnected={isConnected}
				isConnecting={starting}
				statusText={
					isConnected
						? connection?.needs_reconnect
							? t("card_needs_reconnect")
							: connection?.account_email
								? t("connected_as", { email: connection.account_email })
								: t("connected")
						: undefined
				}
				onConnect={configured ? start : undefined}
				onManage={
					connection?.connector_id != null
						? () => onManage(connection.connector_id as number)
						: undefined
				}
			/>
		</div>
	);
}
