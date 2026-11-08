"use client";

import { useTranslations } from "next-intl";
import { toast } from "sonner";
import { ConnectorCard } from "@/components/assistant-ui/connector-popup/components/connector-card";
import { EnumConnectorName } from "@/contracts/enums/connector";
import { useNextcloudConnection } from "../hooks/use-nextcloud-connection";
import { useNextcloudLogin } from "../hooks/use-nextcloud-login";
import { NextcloudLoginPanel } from "./nextcloud-login-panel";

/**
 * Nextcloud in the connectors catalog, next to Google Drive: Connect starts
 * the Login Flow in place; once connected, Manage opens the connection.
 */
export function NextcloudConnectorCard({
	workspaceId,
	onManage,
}: {
	workspaceId: number;
	onManage: (connectorId: number) => void;
}) {
	const t = useTranslations("nextcloud_connect");
	const { connection, isConnected, refresh } = useNextcloudConnection(workspaceId);
	const login = useNextcloudLogin(workspaceId, (next) => {
		toast.success(t("connected_toast"));
		void refresh(next);
		login.reset();
	});
	if (connection && !connection.configured) return null;
	const server = connection?.server ?? "";

	return (
		<div className="flex flex-col gap-2" data-testid="nextcloud-connector-card">
			<ConnectorCard
				id="nextcloud-connector"
				title={t("title")}
				description={t("card_description", { server })}
				connectorType={EnumConnectorName.NEXTCLOUD_CONNECTOR}
				isConnected={isConnected}
				isConnecting={login.state.phase === "starting" || login.state.phase === "waiting"}
				statusText={
					connection?.account
						? connection.needs_reconnect
							? t("card_needs_reconnect")
							: t("connected_as", { name: connection.account.display_name })
						: undefined
				}
				onConnect={login.start}
				onManage={
					connection?.connector_id != null
						? () => onManage(connection.connector_id as number)
						: undefined
				}
			/>
			<NextcloudLoginPanel
				state={login.state}
				server={server}
				onCancel={login.cancel}
				onRetry={login.start}
			/>
		</div>
	);
}
