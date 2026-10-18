"use client";

import { File, X } from "lucide-react";
import { useTranslations } from "next-intl";
import type { FC } from "react";
import { useCallback, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import {
	googleDriveApi,
	type PickedDriveFile,
} from "@/features/google-drive-connect/api/google-drive-api";
import {
	loadGooglePicker,
	pickDriveFiles,
} from "@/features/google-drive-connect/lib/google-picker";
import { GoogleDriveConnectionControls } from "@/features/google-drive-connect/ui/google-drive-connection-controls";
import { AppError } from "@/lib/error";
import type { ConnectorConfigProps } from "../index";

interface SelectedFile {
	id: string;
	name: string;
}

/**
 * The files this Drive connection keeps in sync. Under the drive.file scope
 * the app reaches only files picked in the Google Picker, so files (not
 * folders) are what one picks; saving indexes them, and periodic sync
 * re-reads the ones that changed in Drive.
 */
export const GoogleDriveConfig: FC<ConnectorConfigProps> = ({
	connector,
	onConfigChange,
	workspaceId,
}) => {
	const t = useTranslations("google_drive_connect");
	const [files, setFiles] = useState<SelectedFile[]>(
		(connector.config?.selected_files as SelectedFile[] | undefined) ?? []
	);
	const [busy, setBusy] = useState(false);

	const update = useCallback(
		(next: SelectedFile[]) => {
			setFiles(next);
			onConfigChange?.({ ...connector.config, selected_folders: [], selected_files: next });
		},
		[connector.config, onConfigChange]
	);

	const pick = async () => {
		setBusy(true);
		try {
			const [session] = await Promise.all([
				googleDriveApi.pickerSession(connector.id),
				loadGooglePicker(),
			]);
			const picked: PickedDriveFile[] | null = await pickDriveFiles(session, t("picker_title"));
			if (!picked || picked.length === 0) return;
			const byId = new Map(files.map((f) => [f.id, f]));
			for (const p of picked) byId.set(p.id, { id: p.id, name: p.name });
			update([...byId.values()]);
		} catch (error) {
			toast.error(
				error instanceof AppError && error.code === "google_drive_reconnect"
					? t("needs_reconnect")
					: t("picker_failed")
			);
		} finally {
			setBusy(false);
		}
	};

	return (
		<div className="space-y-4">
			{workspaceId && (
				<GoogleDriveConnectionControls workspaceId={Number(workspaceId)} showDisconnect={false} />
			)}
			<div className="rounded-xl border border-border bg-slate-400/5 dark:bg-white/5 p-3 sm:p-6 space-y-3">
				<div className="space-y-1">
					<h3 className="font-medium text-sm sm:text-base">{t("synced_files_title")}</h3>
					<p className="text-xs sm:text-sm text-muted-foreground">{t("synced_files_hint")}</p>
				</div>
				{files.length > 0 && (
					<ul className="max-h-40 space-y-1 overflow-y-auto rounded-lg bg-muted p-2 text-xs sm:text-sm">
						{files.map((file) => (
							<li
								key={file.id}
								className="flex items-center gap-1.5 text-muted-foreground"
								title={file.name}
							>
								<File className="size-3.5 shrink-0" />
								<span className="flex-1 truncate">{file.name}</span>
								<Button
									type="button"
									variant="ghost"
									size="icon"
									onClick={() => update(files.filter((f) => f.id !== file.id))}
									className="size-5 shrink-0 rounded p-0"
									aria-label={t("remove_file", { name: file.name })}
								>
									<X className="size-3.5" />
								</Button>
							</li>
						))}
					</ul>
				)}
				<Button
					type="button"
					variant="outline"
					onClick={pick}
					disabled={busy}
					className="relative text-xs sm:text-sm h-8 sm:h-9"
				>
					<span className={busy ? "opacity-0" : ""}>
						{files.length > 0 ? t("pick_more_files") : t("pick_files")}
					</span>
					{busy && <Spinner size="xs" className="absolute" />}
				</Button>
			</div>
		</div>
	);
};
