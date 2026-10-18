"use client";

import { Cloud } from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { useNextcloudConnection } from "../hooks/use-nextcloud-connection";
import { useNextcloudLogin } from "../hooks/use-nextcloud-login";
import { NextcloudDisconnectButton } from "./nextcloud-disconnect-button";
import { NextcloudLoginPanel } from "./nextcloud-login-panel";

/**
 * This person's Nextcloud in a workspace: Connect when there is none, and
 * once connected the account with Reconnect and (optionally) Disconnect.
 */
export function NextcloudConnectionControls({
	workspaceId,
	showDisconnect = true,
	onDisconnected,
}: {
	workspaceId: number;
	showDisconnect?: boolean;
	onDisconnected?: () => void;
}) {
	const t = useTranslations("nextcloud_connect");
	const { connection, isLoading, isConnected, refresh } = useNextcloudConnection(workspaceId);
	const login = useNextcloudLogin(workspaceId, (next) => {
		toast.success(t("connected_toast"));
		void refresh(next);
		login.reset();
	});

	if (isLoading || !connection) return null;
	const server = connection.server ?? "";
	if (!connection.configured) {
		return <p className="text-sm text-muted-foreground">{t("not_configured")}</p>;
	}

	const busy = login.state.phase === "starting";
	const flowActive = login.state.phase === "waiting";
	const connectButton = (label: string, variant: "default" | "outline") => (
		<Button
			size="sm"
			variant={variant}
			onClick={login.start}
			disabled={busy || flowActive}
			className="relative"
		>
			<span className={busy ? "opacity-0" : ""}>{label}</span>
			{busy && <Spinner size="xs" className="absolute" />}
		</Button>
	);

	return (
		<div className="flex flex-col gap-3">
			<div className="flex flex-wrap items-center gap-3 rounded-xl border border-border bg-slate-400/5 p-3 dark:bg-white/5">
				<span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-muted">
					<Cloud className="size-4" />
				</span>
				<div className="min-w-0 flex-1 text-sm">
					{isConnected && connection.account ? (
						<>
							<p className="truncate font-medium" data-testid="nextcloud-account">
								{t("connected_as", { name: connection.account.display_name })}
							</p>
							<p className="truncate text-xs text-muted-foreground">
								{t("account_detail", { user: connection.account.user_id, server })}
							</p>
						</>
					) : (
						<>
							<p className="font-medium">{t("title")}</p>
							<p className="text-xs text-muted-foreground">{t("card_description", { server })}</p>
						</>
					)}
				</div>
				<div className="flex flex-wrap items-center gap-1">
					{isConnected
						? connectButton(t("reconnect"), "outline")
						: connectButton(t("connect"), "default")}
					{isConnected && showDisconnect && connection.connector_id != null && (
						<NextcloudDisconnectButton
							connectorId={connection.connector_id}
							onDisconnected={() => {
								void refresh();
								onDisconnected?.();
							}}
						/>
					)}
				</div>
			</div>
			{isConnected && connection.needs_reconnect && !flowActive && (
				<output className="text-xs text-amber-600 dark:text-amber-400">
					{t("needs_reconnect")}
				</output>
			)}
			<NextcloudLoginPanel
				state={login.state}
				server={server}
				onCancel={login.cancel}
				onRetry={login.start}
			/>
		</div>
	);
}
