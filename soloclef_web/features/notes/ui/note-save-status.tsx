"use client";

import { AlertCircle, Check, CloudUpload } from "lucide-react";
import { useTranslations } from "next-intl";
import { Spinner } from "@/components/ui/spinner";
import { cn } from "@/lib/utils";
import type { SaveState } from "../lib/save-queue";
import type { NoteIndexState } from "../model/note";

/**
 * Whether what is on screen is saved, and whether the assistant has caught
 * up with it. Quiet when all is well: a saved note that the assistant has
 * read says only "Saved".
 */
export function NoteSaveStatus({
	saveState,
	indexState,
	onRetry,
	className,
}: {
	saveState: SaveState;
	indexState: NoteIndexState;
	onRetry: () => void;
	className?: string;
}) {
	const t = useTranslations("notes");
	const base = cn(
		"inline-flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground",
		className
	);
	if (saveState === "failed") {
		return (
			<button
				type="button"
				onClick={onRetry}
				className={cn(base, "text-destructive hover:underline")}
			>
				<AlertCircle className="size-3.5" aria-hidden />
				{t("save_failed_retry")}
			</button>
		);
	}
	if (saveState === "saving" || saveState === "unsaved") {
		return (
			<span className={base} aria-live="polite">
				<Spinner size="xs" />
				<span className="hidden sm:inline">{t("saving")}</span>
			</span>
		);
	}
	if (indexState === "pending" || indexState === "processing") {
		return (
			<span className={base} title={t("indexing_hint")} aria-live="polite">
				<CloudUpload className="size-3.5" aria-hidden />
				<span className="hidden sm:inline">{t("indexing")}</span>
			</span>
		);
	}
	if (indexState === "failed") {
		return (
			<span
				className={cn(base, "text-amber-600 dark:text-amber-400")}
				title={t("index_failed_hint")}
			>
				<AlertCircle className="size-3.5" aria-hidden />
				<span className="hidden sm:inline">{t("index_failed")}</span>
			</span>
		);
	}
	return (
		<span className={base} aria-live="polite">
			<Check className="size-3.5" aria-hidden />
			<span className="hidden sm:inline">{t("saved")}</span>
		</span>
	);
}
