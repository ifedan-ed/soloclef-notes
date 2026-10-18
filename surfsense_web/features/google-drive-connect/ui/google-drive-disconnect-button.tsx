"use client";

import { useTranslations } from "next-intl";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { googleDriveApi } from "../api/google-drive-api";

/** Disconnect with an inline confirm; the backend revokes the grant at Google. */
export function GoogleDriveDisconnectButton({
	connectorId,
	onDisconnected,
}: {
	connectorId: number;
	onDisconnected: () => void;
}) {
	const t = useTranslations("google_drive_connect");
	const [confirming, setConfirming] = useState(false);
	const [busy, setBusy] = useState(false);

	const disconnect = async () => {
		setBusy(true);
		try {
			const result = await googleDriveApi.disconnect(connectorId);
			if (result.revoked) toast.success(t("disconnected"));
			else toast.warning(t("disconnected_not_revoked"));
			setConfirming(false);
			onDisconnected();
		} catch {
			toast.error(t("disconnect_failed"));
		} finally {
			setBusy(false);
		}
	};

	if (!confirming) {
		return (
			<Button variant="ghost" size="sm" onClick={() => setConfirming(true)}>
				{t("disconnect")}
			</Button>
		);
	}
	return (
		<div className="flex flex-wrap items-center gap-2 text-xs">
			<span className="text-muted-foreground">{t("disconnect_confirm")}</span>
			<Button
				variant="destructive"
				size="sm"
				onClick={disconnect}
				disabled={busy}
				className="relative"
			>
				<span className={busy ? "opacity-0" : ""}>{t("disconnect_yes")}</span>
				{busy && <Spinner size="xs" className="absolute" />}
			</Button>
			<Button variant="ghost" size="sm" onClick={() => setConfirming(false)} disabled={busy}>
				{t("cancel")}
			</Button>
		</div>
	);
}
