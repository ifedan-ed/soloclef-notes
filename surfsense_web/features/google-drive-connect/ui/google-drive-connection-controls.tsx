"use client";

import { useTranslations } from "next-intl";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { EnumConnectorName } from "@/contracts/enums/connector";
import { getConnectorIcon } from "@/contracts/enums/connectorIcons";
import { useGoogleDriveConnect } from "../hooks/use-google-drive-connect";
import { useGoogleDriveConnection } from "../hooks/use-google-drive-connection";
import { GoogleDriveDisconnectButton } from "./google-drive-disconnect-button";

/**
 * This person's Google Drive in a workspace: Connect when there is none, and
 * once connected the account with Reconnect and (optionally) Disconnect.
 */
export function GoogleDriveConnectionControls({
	workspaceId,
	showDisconnect = true,
	onDisconnected,
}: {
	workspaceId: number;
	showDisconnect?: boolean;
	onDisconnected?: () => void;
}) {
	const t = useTranslations("google_drive_connect");
	const { connection, isLoading, isConnected, refresh } = useGoogleDriveConnection(workspaceId);
	const { start, starting } = useGoogleDriveConnect(workspaceId);

	if (isLoading || !connection) return null;
	if (!connection.configured) {
		return (
			<p className="text-sm text-muted-foreground" data-testid="google-drive-not-configured">
				{t("not_configured")}
			</p>
		);
	}

	const connectButton = (label: string, variant: "default" | "outline") => (
		<Button size="sm" variant={variant} onClick={start} disabled={starting} className="relative">
			<span className={starting ? "opacity-0" : ""}>{label}</span>
			{starting && <Spinner size="xs" className="absolute" />}
		</Button>
	);

	return (
		<div className="flex flex-col gap-3">
			<div className="flex flex-wrap items-center gap-3 rounded-xl border border-border bg-slate-400/5 p-3 dark:bg-white/5">
				<span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-muted">
					{getConnectorIcon(EnumConnectorName.GOOGLE_DRIVE_CONNECTOR, "size-4")}
				</span>
				<div className="min-w-0 flex-1 text-sm">
					{isConnected ? (
						<>
							<p className="truncate font-medium" data-testid="google-drive-account">
								{connection.account_email
									? t("connected_as", { email: connection.account_email })
									: t("connected")}
							</p>
							<p className="truncate text-xs text-muted-foreground">{t("scope_note")}</p>
						</>
					) : (
						<>
							<p className="font-medium">{t("title")}</p>
							<p className="text-xs text-muted-foreground">{t("card_description")}</p>
						</>
					)}
				</div>
				<div className="flex flex-wrap items-center gap-1">
					{isConnected
						? connectButton(t("reconnect"), "outline")
						: connectButton(t("connect"), "default")}
					{isConnected && showDisconnect && connection.connector_id != null && (
						<GoogleDriveDisconnectButton
							connectorId={connection.connector_id}
							onDisconnected={() => {
								void refresh();
								onDisconnected?.();
							}}
						/>
					)}
				</div>
			</div>
			{isConnected && connection.needs_reconnect && (
				<output className="text-xs text-amber-600 dark:text-amber-400">
					{t("needs_reconnect")}
				</output>
			)}
		</div>
	);
}
