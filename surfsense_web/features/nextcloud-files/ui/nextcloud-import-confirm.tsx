"use client";

import { useTranslations } from "next-intl";
import { Button } from "@/components/ui/button";
import type { ImportPreview } from "../api/nextcloud-files-api";
import { formatBytes } from "../lib/picker-selection";

/** The inline "import N files?" step shown before a pick that includes folders. */
export function NextcloudImportConfirm({
	preview,
	busy,
	onConfirm,
	onCancel,
}: {
	preview: ImportPreview;
	busy: boolean;
	onConfirm: () => void;
	onCancel: () => void;
}) {
	const t = useTranslations("nextcloud_files");
	const skipped = preview.unsupported_count + preview.too_large_count;
	return (
		<div
			role="alertdialog"
			aria-live="polite"
			className="space-y-3 rounded-lg border bg-muted/40 p-3 text-sm"
		>
			{preview.over_limit ? (
				<p>{t("confirm_over_limit", { max: preview.max_files })}</p>
			) : preview.file_count === 0 ? (
				<p>{t("confirm_nothing")}</p>
			) : (
				<p>
					{t("confirm_count", {
						count: preview.file_count,
						size: formatBytes(preview.total_bytes),
					})}
				</p>
			)}
			{skipped > 0 ? (
				<p className="text-muted-foreground">{t("confirm_skipped", { count: skipped })}</p>
			) : null}
			<div className="flex flex-wrap justify-end gap-2">
				<Button variant="ghost" onClick={onCancel} disabled={busy}>
					{t("cancel")}
				</Button>
				{!preview.over_limit && preview.file_count > 0 ? (
					<Button onClick={onConfirm} disabled={busy}>
						{t("confirm_import", { count: preview.file_count })}
					</Button>
				) : null}
			</div>
		</div>
	);
}
