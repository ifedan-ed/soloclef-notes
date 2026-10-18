"use client";

import { FileText, Upload } from "lucide-react";
import { useTranslations } from "next-intl";

/** A notebook with no sources yet: what goes here, and a way to add files. */
export function SourcesEmptyState({ onBrowseFiles }: { onBrowseFiles: () => void }) {
	const t = useTranslations("notebook_research");
	return (
		<div className="flex flex-col items-center gap-2 px-4 py-8 text-center">
			<FileText className="size-8 text-muted-foreground" aria-hidden />
			<p className="text-sm font-medium">{t("empty_title")}</p>
			<p className="text-xs text-muted-foreground">{t("empty_description")}</p>
			<button
				type="button"
				onClick={onBrowseFiles}
				className="mt-2 flex w-full items-center justify-center gap-2 rounded-xl border border-dashed px-3 py-4 text-xs text-muted-foreground transition-colors hover:border-ring hover:text-foreground"
			>
				<Upload className="size-4 shrink-0" aria-hidden />
				{t("drop_hint")}
			</button>
		</div>
	);
}
