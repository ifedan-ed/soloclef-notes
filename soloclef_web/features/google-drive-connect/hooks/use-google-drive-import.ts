"use client";

import { useTranslations } from "next-intl";
import { useCallback, useState } from "react";
import { toast } from "sonner";
import { AppError } from "@/lib/error";
import { googleDriveApi } from "../api/google-drive-api";
import { loadGooglePicker, pickDriveFiles } from "../lib/google-picker";
import { driveImportRequest, MAX_PICKED_FILES, pickProblem } from "../lib/picked-files";

/**
 * Pick files in the Google Picker and import them into the account's
 * library, and into the notebook too when one is given.
 */
export function useGoogleDriveImport({
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
	const [busy, setBusy] = useState(false);

	const pickAndImport = useCallback(async () => {
		setBusy(true);
		try {
			const [session] = await Promise.all([
				googleDriveApi.pickerSession(connectorId),
				loadGooglePicker(),
			]);
			const files = await pickDriveFiles(session, t("picker_title"));
			// null is the Picker closed without choosing: nothing to say.
			if (files === null) return;
			// Chosen, but nothing sendable. Say why instead of doing nothing
			// (only folders) or sending a request the server must refuse.
			const problem = pickProblem(files);
			if (problem === "none") {
				toast.info(t("import_nothing_picked"));
				return;
			}
			if (problem === "too_many") {
				toast.error(t("import_too_many", { max: MAX_PICKED_FILES }));
				return;
			}
			const result = await googleDriveApi.importPicked(
				connectorId,
				driveImportRequest({ notebookId, files })
			);
			if (result.queued > 0) toast.success(t("import_started", { count: result.queued }));
			if (result.unsupported.length > 0) {
				toast.warning(t("import_unsupported", { names: result.unsupported.join(", ") }));
			}
			onImported?.();
		} catch (error) {
			if (error instanceof AppError && error.code === "google_drive_reconnect") {
				toast.error(t("needs_reconnect"));
				onNeedsReconnect?.();
			} else {
				toast.error(t("import_failed"));
			}
		} finally {
			setBusy(false);
		}
	}, [connectorId, notebookId, onImported, onNeedsReconnect, t]);

	return { pickAndImport, busy };
}
