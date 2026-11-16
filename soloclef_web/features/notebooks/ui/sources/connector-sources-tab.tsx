"use client";

import Link from "next/link";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui/button";
import { useGoogleDriveConnection } from "@/features/google-drive-connect/hooks/use-google-drive-connection";
import { GoogleDriveConnectionControls } from "@/features/google-drive-connect/ui/google-drive-connection-controls";
import { GoogleDriveImportButton } from "@/features/google-drive-connect/ui/google-drive-import-button";
import { useNextcloudConnection } from "@/features/nextcloud-connect/hooks/use-nextcloud-connection";
import { NextcloudConnectionControls } from "@/features/nextcloud-connect/ui/nextcloud-connection-controls";
import { NextcloudPicker } from "@/features/nextcloud-files/ui/nextcloud-picker";
import { settingsPath } from "@/lib/dashboard-paths";

/**
 * Sources from a connector. Google Drive and Nextcloud are connected right
 * here. Drive's files are picked in the Google Picker (Connect goes to
 * Google's consent page and comes back to this tab); Nextcloud's sign-in
 * opens in a new tab and its files are browsed in place. Either way the
 * picked files are imported and linked to the notebook. Tool connectors
 * (MCP servers, web search) are called by the chat instead.
 */
export function ConnectorSourcesTab({
	notebookId,
	onLeave,
	onPickFromLibrary,
	onImported,
}: {
	notebookId: number;
	onLeave: () => void;
	onPickFromLibrary: () => void;
	onImported: () => void;
}) {
	const t = useTranslations("notebooks");
	const { connection } = useNextcloudConnection();
	const connectorId = connection?.connector_id;
	const usable = connectorId != null && !connection?.needs_reconnect;
	const drive = useGoogleDriveConnection();
	const driveConnectorId = drive.connection?.connector_id;
	const driveUsable = driveConnectorId != null && !drive.connection?.needs_reconnect;

	return (
		<div className="flex flex-col gap-4">
			{/* Each connector only where the server has it set up, as Nextcloud
			    below: a "not set up" line is no use to someone adding sources. */}
			{drive.connection?.configured && (
				<div className="flex flex-col gap-3" data-testid="google-drive-sources">
					<GoogleDriveConnectionControls />
					{driveUsable && (
						<GoogleDriveImportButton
							connectorId={driveConnectorId}
							// The server checks the notebook is the account's own
							// before linking the picks to it.
							notebookId={notebookId}
							onImported={onImported}
							onNeedsReconnect={() => void drive.refresh()}
						/>
					)}
				</div>
			)}
			{connection?.configured && (
				<div className="flex flex-col gap-3" data-testid="nextcloud-sources">
					<NextcloudConnectionControls />
					{usable && (
						<NextcloudPicker
							connectorId={connectorId}
							notebookId={notebookId}
							onImported={onImported}
						/>
					)}
				</div>
			)}
			<div className="flex flex-wrap justify-end gap-2">
				<Button variant="ghost" onClick={onPickFromLibrary}>
					{t("add_from_library")}
				</Button>
				<Button asChild variant="outline">
					<Link href={settingsPath("connectors")} onClick={onLeave}>
						{t("manage_connectors")}
					</Link>
				</Button>
			</div>
		</div>
	);
}
