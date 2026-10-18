"use client";

import { useTranslations } from "next-intl";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { useGoogleDriveImport } from "../hooks/use-google-drive-import";

/** Opens the Google Picker; what is picked is imported (into the notebook, if given). */
export function GoogleDriveImportButton({
	connectorId,
	notebookId,
	onImported,
	onNeedsReconnect,
}: {
	connectorId: number;
	notebookId: number | null;
	onImported?: () => void;
	onNeedsReconnect?: () => void;
}) {
	const t = useTranslations("google_drive_connect");
	const { pickAndImport, busy } = useGoogleDriveImport({
		connectorId,
		notebookId,
		onImported,
		onNeedsReconnect,
	});
	return (
		<Button
			variant="outline"
			onClick={pickAndImport}
			disabled={busy}
			className="relative self-start"
			data-testid="google-drive-pick"
		>
			<span className={busy ? "opacity-0" : ""}>{t("pick_files")}</span>
			{busy && <Spinner size="xs" className="absolute" />}
		</Button>
	);
}
