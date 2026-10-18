"use client";

import { useAtomValue } from "jotai";
import { useTranslations } from "next-intl";
import { type FC, useState } from "react";
import { activeWorkspaceIdAtom } from "@/atoms/workspaces/workspace-query.atoms";
import type { ConnectorConfigProps } from "@/components/assistant-ui/connector-popup/connector-configs";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { NextcloudConnectionControls } from "@/features/nextcloud-connect/ui/nextcloud-connection-controls";

const card = "rounded-xl border border-border bg-slate-400/5 dark:bg-white/5 p-3 sm:p-6 space-y-3";

/**
 * Manage a Nextcloud connection: its name, the connected account and
 * Reconnect. The view's own Disconnect button removes it (and revokes the
 * app password in Nextcloud).
 */
export const NextcloudConnectorConfig: FC<ConnectorConfigProps> = ({ connector, onNameChange }) => {
	const t = useTranslations("nextcloud_connect");
	const workspaceId = useAtomValue(activeWorkspaceIdAtom);
	const [name, setName] = useState(connector.name || "");

	return (
		<div className="space-y-6">
			<div className={card}>
				<Label className="text-xs sm:text-sm">{t("connection_name")}</Label>
				<Input
					value={name}
					onChange={(event) => {
						setName(event.target.value);
						onNameChange?.(event.target.value);
					}}
				/>
			</div>
			{workspaceId != null && (
				<div className={card}>
					<NextcloudConnectionControls workspaceId={Number(workspaceId)} showDisconnect={false} />
					<p className="text-xs text-muted-foreground">{t("how_to_use")}</p>
				</div>
			)}
		</div>
	);
};
