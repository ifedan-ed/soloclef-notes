"use client";

import { Unplug } from "lucide-react";
import Link from "next/link";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui/button";
import { useGoogleDriveConnection } from "@/features/google-drive-connect/hooks/use-google-drive-connection";
import { GoogleDriveConnectionControls } from "@/features/google-drive-connect/ui/google-drive-connection-controls";
import { GoogleDriveImportButton } from "@/features/google-drive-connect/ui/google-drive-import-button";
import { useNextcloudConnection } from "@/features/nextcloud-connect/hooks/use-nextcloud-connection";
import { NextcloudConnectionControls } from "@/features/nextcloud-connect/ui/nextcloud-connection-controls";
import { NextcloudPicker } from "@/features/nextcloud-files/ui/nextcloud-picker";

/**
 * Sources from a connector. Google Drive and Nextcloud are connected right
 * here. Drive's files are picked in the Google Picker (Connect goes to
 * Google's consent page and comes back to this tab); Nextcloud's sign-in
 * opens in a new tab and its files are browsed in place. Either way the
 * picked files are imported and linked to the notebook. Tool connectors
 * (MCP servers, web search) are called by the chat instead.
 */
export function ConnectorSourcesTab({
	workspaceId,
	notebookId,
	onLeave,
	onPickFromLibrary,
	onImported,
}: {
	workspaceId: number;
	notebookId: number;
	onLeave: () => void;
	onPickFromLibrary: () => void;
	onImported: () => void;
}) {
	const t = useTranslations("notebooks");
	const { connection } = useNextcloudConnection(workspaceId);
	const connectorsHref = `/dashboard/${workspaceId}/workspace-settings/connectors`;
	const connectorId = connection?.connector_id;
	const usable = connectorId != null && !connection?.needs_reconnect;
	const drive = useGoogleDriveConnection(workspaceId);
	const driveConnectorId = drive.connection?.connector_id;
	const driveUsable = driveConnectorId != null && !drive.connection?.needs_reconnect;

	return (
		<div className="flex flex-col gap-4">
			<div className="flex flex-col gap-3" data-testid="google-drive-sources">
				<GoogleDriveConnectionControls workspaceId={workspaceId} />
				{driveUsable && (
					<>
						<p className="text-sm text-muted-foreground">{t("google_drive_picker_hint")}</p>
						<GoogleDriveImportButton
							connectorId={driveConnectorId}
							notebookId={notebookId}
							onImported={onImported}
							onNeedsReconnect={() => void drive.refresh()}
						/>
					</>
				)}
			</div>
			{connection?.configured && (
				<div className="flex flex-col gap-3" data-testid="nextcloud-sources">
					<NextcloudConnectionControls workspaceId={workspaceId} />
					{usable && (
						<>
							<p className="text-sm text-muted-foreground">{t("nextcloud_picker_hint")}</p>
							<NextcloudPicker
								connectorId={connectorId}
								notebookId={notebookId}
								onImported={onImported}
							/>
						</>
					)}
				</div>
			)}
			<div className="flex flex-col gap-4 rounded-lg border p-4 text-sm">
				<div className="flex items-start gap-3">
					<span className="flex size-9 shrink-0 items-center justify-center rounded-full bg-muted">
						<Unplug className="size-4" />
					</span>
					<div className="space-y-2">
						<p className="font-medium">{t("connector_sources_title")}</p>
						<p className="text-muted-foreground">{t("connector_sources_files")}</p>
						<p className="text-muted-foreground">{t("connector_sources_tools")}</p>
					</div>
				</div>
				<div className="flex flex-wrap justify-end gap-2">
					<Button variant="ghost" onClick={onPickFromLibrary}>
						{t("add_from_library")}
					</Button>
					<Button asChild variant="outline">
						<Link href={connectorsHref} onClick={onLeave}>
							{t("manage_connectors")}
						</Link>
					</Button>
				</div>
			</div>
		</div>
	);
}
